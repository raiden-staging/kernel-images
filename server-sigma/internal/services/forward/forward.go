package forward

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"kernel-operator-api/internal/utils"
)

type forwardItem struct {
	id        string
	listener  net.Listener
	fromPort  int
	toPort    int
	dir       string
	stopCh    chan struct{}
}

var (
	mu       sync.Mutex
	forwards = map[string]*forwardItem{}
)

func Add(direction string, hostPort, vmPort int) (map[string]any, error) {
	id := utils.UID()
	from := hostPort
	to := vmPort
	if direction != "host_to_vm" && direction != "vm_to_host" {
		return nil, errors.New("invalid direction")
	}
	// For both directions, we listen on "from" and forward to "to" on localhost.
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", from))
	if err != nil {
		return nil, err
	}
	item := &forwardItem{id: id, listener: ln, fromPort: from, toPort: to, dir: direction, stopCh: make(chan struct{})}
	mu.Lock(); forwards[id] = item; mu.Unlock()
	go serve(item)
	return map[string]any{"forward_id": id, "active": true}, nil
}

func serve(it *forwardItem) {
	for {
		conn, err := it.listener.Accept()
		if err != nil {
			select {
			case <-it.stopCh:
				return
			default:
				continue
			}
		}
		go func(c net.Conn) {
			dst, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", it.toPort))
			if err != nil {
				_ = c.Close()
				return
			}
			// bidirectional copy
			go io.Copy(dst, c)
			go io.Copy(c, dst)
		}(conn)
	}
}

func Remove(id string) error {
	mu.Lock(); it := forwards[id]; delete(forwards, id); mu.Unlock()
	if it == nil {
		return errors.New("Not Found")
	}
	close(it.stopCh)
	_ = it.listener.Close()
	return nil
}