//go:build windows
// +build windows

package main

import (
	"bufio"
	_ "embed"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"reflect"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/apenwarr/fixconsole"
	"github.com/getlantern/systray"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	unixSocket  = flag.String("wsl", "", "Path to Unix socket for passthrough to WSL")
	tcpSocket   = flag.String("tcp", "", "Host/Port to listen for TCP")
	namedPipe   = flag.String("winssh", "", "Named pipe for use with Win32 OpenSSH")
	verbose     = flag.Bool("verbose", false, "Enable verbose logging")
	systrayFlag = flag.Bool("systray", false, "Enable systray integration")
	force       = flag.Bool("force", false, "Force socket usage (unlink existing socket)")
)

const (
	// Windows constats
	invalidHandleValue = ^windows.Handle(0)
	pageReadWrite      = 0x4
	fileMapWrite       = 0x2

	// ssh-agent/Pageant constants
	agentMaxMessageLength = 8192
	agentCopyDataID       = 0x804e50ba
)

// copyDataStruct is used to pass data in the WM_COPYDATA message.
// We directly pass a pointer to our copyDataStruct type, we need to be
// careful that it matches the Windows type exactly
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

type SecurityAttributes struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      uint32
}

var queryPageantMutex sync.Mutex

//go:embed assets/icon.ico
var icon []byte

func makeInheritSaWithSid() *windows.SecurityAttributes {
	var sa windows.SecurityAttributes

	u, err := user.Current()

	if err == nil {
		sd, err := windows.SecurityDescriptorFromString("O:" + u.Uid)
		if err == nil {
			sa.SecurityDescriptor = sd
		}
	}

	sa.Length = uint32(unsafe.Sizeof(sa))

	sa.InheritHandle = 1

	return &sa

}

func queryPageant(buf []byte) (result []byte, err error) {
	if len(buf) > agentMaxMessageLength {
		err = errors.New("message too long")
		return
	}

	name, _ := syscall.UTF16PtrFromString("Pageant")
	hwnd := win.FindWindow(name, name)
	if hwnd == 0 {
		log.Println("launching gpg-connect-agent")
		cmd := exec.Command("gpg-connect-agent", "/bye")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Run()

		name, _ = syscall.UTF16PtrFromString("Pageant")
		hwnd = win.FindWindow(name, name)
		if hwnd == 0 {
			err = errors.New("could not find Pageant window")
			return
		}
	}

	// Typically you'd add thread ID here but thread ID isn't useful in Go
	// We would need goroutine ID but Go hides this and provides no good way of
	// accessing it, instead we serialise calls to queryPageant and treat it
	// as not being goroutine safe
	mapName := "WSLPageantRequest"
	queryPageantMutex.Lock()

	var sa = makeInheritSaWithSid()

	mapNamePtr, _ := syscall.UTF16PtrFromString(mapName)
	fileMap, err := windows.CreateFileMapping(invalidHandleValue, sa, pageReadWrite, 0, agentMaxMessageLength, mapNamePtr)
	if err != nil {
		queryPageantMutex.Unlock()
		return
	}
	defer func() {
		windows.CloseHandle(fileMap)
		queryPageantMutex.Unlock()
	}()

	sharedMemory, err := windows.MapViewOfFile(fileMap, fileMapWrite, 0, 0, 0)
	if err != nil {
		return
	}
	defer windows.UnmapViewOfFile(sharedMemory)

	sharedMemoryArray := (*[agentMaxMessageLength]byte)(unsafe.Pointer(sharedMemory))
	copy(sharedMemoryArray[:], buf)

	mapNameWithNul := mapName + "\000"

	// We use our knowledge of Go strings to get the length and pointer to the
	// data and the length directly
	cds := copyDataStruct{
		dwData: agentCopyDataID,
		cbData: uint32(((*reflect.StringHeader)(unsafe.Pointer(&mapNameWithNul))).Len),
		lpData: ((*reflect.StringHeader)(unsafe.Pointer(&mapNameWithNul))).Data,
	}

	ret := win.SendMessage(hwnd, win.WM_COPYDATA, 0, uintptr(unsafe.Pointer(&cds)))
	if ret == 0 {
		err = errors.New("WM_COPYDATA failed")
		return
	}

	len := binary.BigEndian.Uint32(sharedMemoryArray[:4])
	len += 4

	if len > agentMaxMessageLength {
		err = errors.New("return message too long")
		return
	}

	result = make([]byte, len)
	copy(result, sharedMemoryArray[:len])

	return
}

var failureMessage = [...]byte{0, 0, 0, 1, 5}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(reader, lenBuf)
		if err != nil {
			if *verbose {
				log.Printf("header io.ReadFull error '%s'", err)
			}
			return
		}

		len := binary.BigEndian.Uint32(lenBuf)
		buf := make([]byte, len)
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			if *verbose {
				log.Printf("body io.ReadFull error '%s'", err)
			}
			return
		}

		result, err := queryPageant(append(lenBuf, buf...))
		if err != nil {
			// If for some reason talking to Pageant fails we fall back to
			// sending an agent error to the client
			if *verbose {
				log.Printf("Pageant query error '%s'", err)
			}
			result = failureMessage[:]
		}

		_, err = conn.Write(result)
		if err != nil {
			if *verbose {
				log.Printf("net.Conn.Write error '%s'", err)
			}
			return
		}
	}
}

func listenLoop(ln net.Listener) {
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("net.Listener.Accept error '%s'", err)
			return
		}

		if *verbose {
			log.Printf("New connection: %v\n", conn)
		}

		go handleConnection(conn)
	}
}

func main() {
	fixconsole.FixConsoleIfNeeded()
	flag.Parse()

	var unix, pipe net.Listener
	var err error

	done := make(chan bool, 1)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		switch sig {
		case os.Interrupt:
			log.Printf("Caught signal")
			done <- true
		}
	}()

	if *unixSocket != "" {
		_, err := os.Stat(*unixSocket)
		if err == nil || !os.IsNotExist(err) {
			if *force {
				// If the socket file already exists then unlink it
				err = syscall.Unlink(*unixSocket)
				if err != nil {
					log.Fatalf("Failed to unlink socket %s, error '%s'\n", *unixSocket, err)
				}
			} else {
				log.Fatalf("Error: the SSH_AUTH_SOCK file already exists. Please delete it manually or use --force option.")
			}
		}

		unix, err = net.Listen("unix", *unixSocket)
		if err != nil {
			log.Fatalf("Could not open socket %s, error '%s'\n", *unixSocket, err)
		}

		defer unix.Close()
		log.Printf("Listening on Unix socket: %s\n", *unixSocket)
		go func() {
			listenLoop(unix)

			// If for some reason our listener breaks, kill the program
			done <- true
		}()
	}

	if *tcpSocket != "" {
		tcp, err := net.Listen("tcp", *tcpSocket)
		if err != nil {
			log.Fatalf("Could not open socket %s, error '%s'\n", *tcpSocket, err)
		}

		defer tcp.Close()
		log.Printf("Listening on Tcp socket: %s\n", *tcpSocket)
		go func() {
			listenLoop(tcp)

			// If for some reason our listener breaks, kill the program
			done <- true
		}()
	}

	if *namedPipe != "" {
		namedPipeFullName := "\\\\.\\pipe\\" + *namedPipe
		var cfg = &winio.PipeConfig{}
		pipe, err = winio.ListenPipe(namedPipeFullName, cfg)
		if err != nil {
			log.Fatalf("Could not open named pipe %s, error '%s'\n", namedPipeFullName, err)
		}

		defer pipe.Close()
		log.Printf("Listening on named pipe: %s\n", namedPipeFullName)
		go func() {
			listenLoop(pipe)

			// If for some reason our listener breaks, kill the program
			done <- true
		}()
	}

	if *namedPipe == "" && *unixSocket == "" && *tcpSocket == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *systrayFlag {
		go func() {
			// Wait until we are signalled as finished
			<-done

			// If for some reason our listener breaks, kill the program
			systray.Quit()
		}()

		systray.Run(onSystrayReady, nil)

		log.Print("Exiting...")
	} else {
		// Wait until we are signalled as finished
		<-done

		log.Print("Exiting...")
	}
}

func onSystrayReady() {
	systray.SetTitle("WSL-SSH-Pageant")
	systray.SetTooltip("WSL-SSH-Pageant")
	systray.SetIcon(icon)

	quit := systray.AddMenuItem("Quit", "Quits this app")

	go func() {
		for range quit.ClickedCh {
			systray.Quit()
			return
		}
	}()
}
