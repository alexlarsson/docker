package nsinit

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"syscall"

	"github.com/dotcloud/docker/pkg/libcontainer"
)

// SyncPipe allows communication to and from the child processes
// to it's parent and allows the two independent processes to
// syncronize their state.
type SyncPipe struct {
	child      *os.File
	parentConn *net.UnixConn
}

func NewSyncPipe() (s *SyncPipe, err error) {
	pair, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM|syscall.FD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	childFile := os.NewFile(uintptr(pair[0]), "")
	parentFile := os.NewFile(uintptr(pair[1]), "")
	defer parentFile.Close()

	conn, err := net.FileConn(parentFile)
	if err != nil {
		childFile.Close()
		return nil, err
	}

	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		childFile.Close()
		conn.Close()
		return nil, fmt.Errorf("%d: not a unix connection", parentFile.Fd())
	}

	return &SyncPipe{child: childFile, parentConn: uconn}, nil
}

func NewSyncPipeFromChildFd(childFd uintptr) (*SyncPipe, error) {
	childFile := os.NewFile(childFd, "childPipe")

	// We avoid using a *net.UnixConn in the child, because that starts up
	// the epoll service which gives warnings when we later closes its fd

	return &SyncPipe{child: childFile}, nil
}

func (s *SyncPipe) Child() *os.File {
	return s.child
}

func (s *SyncPipe) ParentConn() *net.UnixConn {
	return s.parentConn
}

func (s *SyncPipe) SendToChild(context libcontainer.Context) error {
	data, err := json.Marshal(context)
	if err != nil {
		return err
	}
	s.parentConn.Write(data)
	return nil
}

func (s *SyncPipe) ReadFromParent() (libcontainer.Context, error) {
	data, err := ioutil.ReadAll(s.child)
	if err != nil {
		return nil, fmt.Errorf("error reading from sync pipe %s", err)
	}
	var context libcontainer.Context
	if len(data) > 0 {
		if err := json.Unmarshal(data, &context); err != nil {
			return nil, err
		}
	}
	return context, nil
}

func (s *SyncPipe) SendFdsToParent(files []*os.File) error {
	var fds []int
	for _, f := range files {
		fds = append(fds, int(f.Fd()))
	}
	oob := syscall.UnixRights(fds...)
	err := syscall.Sendmsg(int(s.child.Fd()), []byte("x"), oob, nil, 0)
	if err != nil {
		return err
	}

	return nil
}

func extractFds(oob []byte) ([]*os.File, error) {
	// Grab forklock to make sure no forks accidentally inherit the new
	// fds before they are made CLOEXEC
	// There is a slight race condition between ReadMsgUnix returns and
	// when we grap the lock, so this is not perfect. Unfortunately
	// There is no way to pass MSG_CMSG_CLOEXEC to recvmsg() nor any
	// way to implement non-blocking i/o in go, so this is hard to fix.
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()
	scms, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}

	files := []*os.File{}

	for _, scm := range scms {
		fds, err := syscall.ParseUnixRights(&scm)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			syscall.CloseOnExec(fd)
			files = append(files, os.NewFile(uintptr(fd), ""))
		}
	}

	return files, nil
}

func (s *SyncPipe) ReadFdsFromChild() ([]*os.File, error) {
	oob := make([]byte, syscall.CmsgSpace(1024))
	buf := make([]byte, 1024)
	_, oobn, _, _, err := s.parentConn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}
	return extractFds(oob[:oobn])
}

func (s *SyncPipe) CloseWrite() error {
	return s.parentConn.CloseWrite()
}

func (s *SyncPipe) Close() error {
	if s.parentConn != nil {
		s.parentConn.Close()
	}
	if s.child != nil {
		s.child.Close()
	}
	return nil
}
