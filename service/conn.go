package service

import (
	"net"
	"reflect"
	"sync/atomic"
)

type connManager interface {
	addConn(*Conn)
	delConn(*Conn)
}

type Conn struct {
	net.Conn
	fd      int
	closed  atomic.Bool
	manager connManager
}

func newConn(conn net.Conn, manager connManager) *Conn {
	return &Conn{
		Conn:    conn,
		fd:      fdFromConn(conn),
		manager: manager,
	}
}

// Close closes net.Conn and prevents repeated connection close.
func (c *Conn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		return c.Conn.Close()
	}
	return nil
}

// fdFromConn extracts fd from net.Conn.
func fdFromConn(conn net.Conn) int {
	tcpConn := reflect.Indirect(reflect.ValueOf(conn)).FieldByName("conn")
	fdVal := tcpConn.FieldByName("fd")
	pfdVal := reflect.Indirect(fdVal).FieldByName("pfd")
	return int(pfdVal.FieldByName("Sysfd").Int())
}
