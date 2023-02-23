package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

// frontend is ...
type frontend struct {
	ctx         context.Context
	logger      *zerolog.Logger
	app         *application
	laddr       *net.TCPAddr
	tcpListener *net.TCPListener
	rmu         sync.RWMutex
	connections map[int]*Conn
	bufPool     *sync.Pool
}

var _ connManager = (*frontend)(nil)

func newFrontend(ctx context.Context, logger *zerolog.Logger, port int, app *application, bufPool *sync.Pool) (*frontend, error) {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, errors.Wrap(err, "ResolveTCPAddr()")
	}
	return &frontend{
		ctx:         ctx,
		logger:      logger,
		app:         app,
		laddr:       addr,
		connections: make(map[int]*Conn),
		bufPool:     bufPool,
	}, nil
}

// addConn adds new connection to the connections map or closes this connection.
func (f *frontend) addConn(conn *Conn) {
	select {
	case <-f.ctx.Done():
		conn.Close()
	default:
	}
	f.rmu.Lock()
	defer f.rmu.Unlock()
	f.connections[conn.fd] = conn

}

// delConn deletes connection from the connections map or does nothing.
func (f *frontend) delConn(conn *Conn) {
	select {
	case <-f.ctx.Done():
		return
	default:
	}
	f.rmu.Lock()
	defer f.rmu.Unlock()
	delete(f.connections, conn.fd)
}

// run is a blocking function. It tries to create tcpListener.
// It starts listenForNewConn goroutine.
// It exits on ctx is done and closes tcpListener and all connections.
func (f *frontend) run(wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		tcpListener, err := net.ListenTCP("tcp", f.laddr)
		if err != nil {
			f.logger.Error().Err(err).Str("frontend", f.laddr.String()).Msg("ListenTCP()")
			time.Sleep(5 * time.Second)
			continue
		}
		f.tcpListener = tcpListener
		break
	}

	go f.listenForNewConn()

	// waiting for the graceful shutdown. after this it closes the listener and closes connections
	<-f.ctx.Done()
	f.logger.Info().Str("frontend", f.laddr.String()).Msg("closing listener and connections")

	f.tcpListener.Close()

	f.rmu.RLock()
	defer f.rmu.RUnlock()
	for _, conn := range f.connections {
		conn.Close()
	}
}

// listenForNewConn is a blocking function. It is responsible for accepting new incoming connections.
func (f *frontend) listenForNewConn() {
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		netConn, err := f.tcpListener.AcceptTCP()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			f.logger.Info().Err(err).Str("frontend", f.laddr.String()).Msg("AcceptTCP()")
			continue
		}

		f.logger.Debug().Str("frontend", f.laddr.String()).Str("connection", netConn.RemoteAddr().String()).Msg("accepted new connection")

		go f.handleNewConnection(netConn)
	}
}

// handleNewConnection processes new incoming connections. It tries to find available backend and create remote connection.
// This function creates TWO goroutines to transfer data between incoming and outgoing connections.
func (f *frontend) handleNewConnection(netConn *net.TCPConn) {
	// creating a remote connection for the local connection
	rConn, err := f.app.createRemoteConnection()
	if err != nil {
		f.logger.Error().Err(err).Str("frontend", f.laddr.String()).Msg("can't find next backend")
		f.logger.Debug().Msgf("closing connection %s -> %s", netConn.RemoteAddr().String(), netConn.LocalAddr().String())
		netConn.Close()
		return
	}
	rConn.manager.addConn(rConn)

	conn := newConn(netConn, f)
	conn.manager.addConn(conn)

	closeOnce := sync.Once{}
	closeAll := func() {
		f.logger.Debug().Msgf("closing connection %s -> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
		conn.Close()
		f.logger.Debug().Msgf("closing connection %s -> %s", rConn.LocalAddr().String(), rConn.RemoteAddr().String())
		rConn.Close()
		conn.manager.delConn(conn)
		rConn.manager.delConn(rConn)
	}

	go func() {
		buf := f.getBuf()
		defer f.bufPool.Put(buf)
		for {
			n, err := io.CopyBuffer(conn, rConn, *buf)
			if err != nil {
				f.logger.Info().Err(err).Msgf("can't copy data %s -> %s", rConn.RemoteAddr().String(), conn.LocalAddr().String())
				break
			}
			if n == 0 {
				break
			}
		}
		closeOnce.Do(closeAll)
	}()

	go func() {
		buf := f.getBuf()
		defer f.bufPool.Put(buf)
		for {
			n, err := io.CopyBuffer(rConn, conn, *buf)
			if err != nil {
				f.logger.Info().Err(err).Msgf("can't copy data %s -> %s", conn.LocalAddr().String(), rConn.RemoteAddr().String())
				break
			}
			if n == 0 {
				break
			}
		}
		closeOnce.Do(closeAll)
	}()
}

func (f *frontend) getBuf() *[]byte {
	return f.bufPool.Get().(*[]byte)
}
