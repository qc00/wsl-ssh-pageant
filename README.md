# wsl-ssh-pageant

## Why
Because [benpye/wsl-ssh-pageant](https://github.com/benpye/wsl-ssh-pageant/) has not been maintained for a long time, so I forked it and maintaining this project.  

## How to use with WSL

1. On the Windows side start Pageant (or compatible agent such as gpg4win).

2. Run `wsl-ssh-pageant.exe --wsl C:\wsl-ssh-pageant\ssh-agent.sock` (or any other path, max ~100 characters)

3. In WSL export the `SSH_AUTH_SOCK` environment variable to point at the socket, for example, if you have `ssh-agent.sock` in `C:\wsl-ssh-pageant`
```
$ export SSH_AUTH_SOCK=/mnt/c/wsl-ssh-pageant/ssh-agent.sock
```

4. The SSH keys from Pageant should now be usable by `ssh`

## How to use with Windows 10 native OpenSSH client

1. On the Windows side start Pageant (or compatible agent such as gpg4win).

2. Run `wsl-ssh-pageant.exe --winssh ssh-pageant` (or any other name)

3. In `cmd` export the `SSH_AUTH_SOCK` environment variable or define it in your Environment Variables on Windows. Use the name you gave the pipe, for example:

```
$ set SSH_AUTH_SOCK=\\.\pipe\ssh-pageant
```

4. The SSH keys from Pageant should now be usable by the native Windows SSH client, try using `ssh` in `cmd.exe`

## Systray Integration

To add an icon to the systray run `wsl-ssh-pageant.exe --systray --winssh ssh-pageant` (or using `--wsl`).

## Note

You can use both `--winssh` and `--wsl` parameters at the same time with the same process to proxy for both

# Frequently asked questions

## How do I download it?
Grab the latest release on the [releases page](https://github.com/AkinoKaede/wsl-ssh-pageant/releases).

## How do I build this?
You need Go 1.16 or later.

To create a build with a console window:
```
go build
```

To create a build without a console window:
```
go build -ldflags -H=windowsgui
```

## What version of Windows do I need?
You need Windows 10 1803 or later for WSL support as it is the first version supporting `AF_UNIX` sockets. You can still use this with the native [Windows SSH client](https://github.com/PowerShell/Win32-OpenSSH/releases) on earlier builds.

## The -gui.exe binary doesn't have a GUI? (immediately closes)
The difference between the gui.exe binary and the regular binaries is the subsystem as set in the PE header. The gui.exe binary is set with the Win32 subsystem so that it doesn't spawn a command line, allowing it to be launched on startup. The regular binary has the console subsystem so it does launch a command line if double clicked, and will block the command line as expected. Note: You may launch either binary with the `-systray` flag to have a systray icon whilst the tool is running, this only provides a way to quit the application.

## You didn't answer my question!
Please open an issue, I do try and keep on top of them, promise.

# Credit

* Thanks to [Ben Pye](https://github.com/benpye/) for [benpye/wsl-ssh-pageant](https://github.com/benpye/wsl-ssh-pageant/)
* Thanks to [John Starks](https://github.com/jstarks/) for [npiperelay](https://github.com/jstarks/npiperelay/) for an example of a more secure way to create a stream between WSL and Linux before `AF_UNIX` sockets were available.
* Thanks for [Mark Dietzer](https://github.com/Doridian) for several contributions to the old .NET implementation.
