package mcpserver

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// samePeer refuses connections from processes of another user.
func samePeer(c net.Conn) error {
	raw, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if cerr == nil {
			uid = cred.Uid
		}
	}); err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if int(uid) != os.Getuid() {
		return errors.New("the bridge runs as another user")
	}
	return nil
}
