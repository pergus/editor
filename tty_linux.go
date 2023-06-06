package editor


import (
	"golang.org/x/sys/unix"
)


func enableRawMode() error {
        termios, err := unix.IoctlGetTermios(unix.Stdin, unix.TCGETS)
        if err != nil {
                return err
        }

        editor.orgTermios = *termios

        /* Disable ctrl-S, ctrl-Q and ctrl-M. */
        termios.Iflag = termios.Iflag &^ (unix.IXON | unix.ICRNL | unix.BRKINT | unix.INPCK | unix.ISTRIP)

        /* Disable ECHO, Canonical Mode, ctrl-C, ctrl-Z, ctrl-V and ctrl-O */
        termios.Lflag = termios.Lflag &^ (unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN)

        /* Disable all output processing */
        termios.Oflag = termios.Oflag &^ (unix.OPOST)

        /* Set the character size (CS) to 8 bits per byte. */
        termios.Cflag |= (unix.CS8)

        /* The VMIN value sets the minimum number of bytes of input needed before read() can return.
        We set it to 0 so that read() returns as soon as there is any input to be read.*/
        termios.Cc[unix.VMIN] = 0
        /* The VTIME value sets the maximum amount of time to wait before read() returns.
        It is in tenths of a second, so we set it to 1/10 of a second, or 100 milliseconds.*/
        termios.Cc[unix.VTIME] = 1

        if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETSF, termios); err != nil {
                return err
        }

        return nil
}

func disableRawMode() error {
        if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETSF, &editor.orgTermios); err != nil {
                return err
        }

        return nil
}

