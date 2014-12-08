// The pool package implements a connection pool for redis connections which is
// thread-safe
package pool

import (
	"github.com/fzzy/radix/redis"
)

// A simple connection pool. It will create a small pool of initial connections,
// and if more connections are needed they will be created on demand. If a
// connection is returned and the pool is full it will be closed.
type Pool struct {
	Network string
	Addr    string
	Pool    chan *redis.Client
}

// Creates a new Pool whose connections are all created using
// redis.Dial(network, addr). The size indicates the maximum number of idle
// connections to have waiting to be used at any given moment
func NewPool(network, addr string, size int) (*Pool, error) {
	var err error
	pool := make([]*redis.Client, size)
	for i := range pool {
		if pool[i], err = redis.Dial(network, addr); err != nil {
			return nil, err
		}
	}
	p := Pool{
		Network: network,
		Addr:    addr,
		Pool:    make(chan *redis.Client, len(pool)),
	}
	for i := range pool {
		p.Pool <- pool[i]
	}
	return &p, nil
}

// Calls NewPool, but if there is an error it return a pool of the same size but
// without any connections pre-initialized (can be used the same way, but if
// this happens there might be something wrong with the redis instance you're
// connecting to)
func NewOrEmptyPool(network, addr string, size int) *Pool {
	pool, err := NewPool(network, addr, size)
	if err != nil {
		pool = &Pool{
			Network: network,
			Addr:    addr,
			Pool:    make(chan *redis.Client, size),
		}
	}
	return pool
}

// Retrieves an available redis client. If there are none available it will
// create a new one on the fly
func (p *Pool) Get() (*redis.Client, error) {
	select {
	case conn := <-p.Pool:
		return conn, nil
	default:
		conn, err := redis.Dial(p.Network, p.Addr)
		p.CarefullyPut(conn, &err)
		return conn, err
	}
}

// Returns a client back to the pool. If the pool is full the client is closed
// instead. If the client is already closed (due to connection failure or
// what-have-you) it should not be put back in the pool. The pool will create
// more connections as needed.
func (p *Pool) Put(conn *redis.Client) {
	select {
	case p.Pool <- conn:
	default:
		conn.Close()
	}
}

// A useful helper method which acts as a wrapper around Put. It will only
// actually Put the conn back if potentialErr is not an error or is a
// redis.CmdError. It would be used like the following:
//
//	func doSomeThings(p *Pool) error {
//		conn, redisErr := p.Get()
//		if redisErr != nil {
//			return redisErr
//		}
//		defer p.CarefullyPut(conn, &redisErr)
//
//		var i int
//		i, redisErr = conn.Cmd("GET", "foo").Int()
//		if redisErr != nil {
//			return redisErr
//		}
//
//		redisErr = conn.Cmd("SET", "foo", i * 3).Err
//		return redisErr
//	}
//
// If we were just using the normal Put we wouldn't be able to defer it because
// we don't want to Put back a connection which is broken. This method takes
// care of doing that check so we can still use the convenient defer
func (p *Pool) CarefullyPut(conn *redis.Client, potentialErr *error) {
	if potentialErr != nil && *potentialErr != nil {
		// We don't care about command errors, they don't indicate anything
		// about the connection integrity
		if _, ok := (*potentialErr).(*redis.CmdError); !ok {
			return
		}
	}
	p.Put(conn)
}

// Removes and calls Close() on all the connections currently in the pool.
// Assuming there are no other connections waiting to be Put back this method
// effectively closes and cleans up the pool.
func (p *Pool) Empty() {
	var conn *redis.Client
	for {
		select {
		case conn = <-p.Pool:
			conn.Close()
		default:
			return
		}
	}
}
