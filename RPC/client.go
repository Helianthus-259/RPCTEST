package RPC

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type Call struct {
	Seq           uint64
	ServiceMethod string     // 要调用的服务名称和方法
	Args          any        // The argument to the function (*struct).
	Reply         any        // The reply from the function (*struct).
	Error         error      // After completion, the error status.
	Done          chan *Call // 当调用结束时，会调用 call.done() 通知调用方
}

func (call *Call) done() {
	select {
	case call.Done <- call:
		// ok
	default:
		log.Println("rpc: discarding Call reply due to insufficient Done chan capacity")
	}
}

type Client struct {
	codec       MyJsonCodec //编解码器
	headerMutex sync.Mutex  // protects following
	header      Header
	mutex       sync.Mutex // protects following
	seq         uint64
	pending     map[uint64]*Call
	closing     bool // user has called Close
	shutdown    bool // server has told us to stop
}

var ErrShutdown = errors.New("connection is shut down")

func (client *Client) Close() error {
	client.mutex.Lock()
	if client.closing { // 如果连接已经关闭时，返回ErrShutdown
		client.mutex.Unlock()
		return ErrShutdown
	}
	client.closing = true
	client.mutex.Unlock()
	//log.Println("我关了")
	return client.codec.Close() // 关闭时调用编解码器的关闭方法
}

func (client *Client) IsAvailable() bool {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return !client.shutdown && !client.closing //判断客户端是否正常工作
}

func (client *Client) registerCall(call *Call) (uint64, error) { //注册Call
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if client.closing || client.shutdown { //假如客户端已经关闭，返回错误
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

func (client *Client) removeCall(seq uint64) *Call {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

func (client *Client) Send(call *Call) {
	client.headerMutex.Lock()
	defer client.headerMutex.Unlock()

	// 注册Call
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		log.Println("rpc: Register Call Error :", err)
	}

	// 用MyJSONCodec编码器编码并发送请求
	client.header.Seq = seq
	client.header.ServiceMethod = call.ServiceMethod
	err = client.codec.Write(&client.header, call.Args)
	if err != nil {
		client.mutex.Lock()
		call = client.pending[seq]
		delete(client.pending, seq)
		client.mutex.Unlock()
		if call != nil {
			call.Error = err
			call.done()
			log.Println("rpc: WriteRequest Error :", err)
		}
	}
}

func (client *Client) Receive() {
	var err error
	var header Header
	// 循环等待接收
	for err == nil {
		header = Header{}
		//读响应的header
		err = client.codec.ReadHeader(&header)
		if err != nil {
			break
		}
		seq := header.Seq
		call := client.removeCall(seq) //从pending中移除call，并返回删除的call

		switch {
		case call == nil:
			// call不存在，意味着WriteRequest失败，请求已经被删除了
			err = client.codec.ReadBody(nil)
			if err != nil {
				err = errors.New("reading error body: " + err.Error())
			}
		case header.Error != "":
			// call 存在，但服务端处理出错，那么应该将错误信息传递给请求然后结束请求
			call.Error = errors.New(header.Error)
			err = client.codec.ReadBody(nil)
			if err != nil {
				err = errors.New("reading error body: " + err.Error())
			}
			call.done()
		default:
			// 假如没有异常，那么call 存在，服务端处理正常，那么需要从 body 中读取 Reply 的值
			err = client.codec.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	// 假如error出现，那么终止挂起的请求，client也由于异常关闭
	client.headerMutex.Lock()
	client.mutex.Lock()
	client.shutdown = true //client也由于异常关闭
	closing := client.closing
	if err == io.EOF {
		if closing {
			err = ErrShutdown
		} else {
			err = io.ErrUnexpectedEOF
		}
	}
	// 遍历pending终止请求
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
	client.mutex.Unlock()
	client.headerMutex.Unlock()
	if err != io.EOF && !closing {
		log.Println("rpc: client protocol error:", err)
	}
}

func NewClient(conn io.ReadWriteCloser) *Client {
	codec := NewMyCodec(conn)
	return NewClientWithCodec(codec)
}

func NewClientWithTimeout(conn io.ReadWriteCloser) *Client {
	codec := NewMyCodec(conn)
	return NewClientWithCodecAndTimeout(codec)
}

func NewClientWithCodec(codec MyJsonCodec) *Client {
	client := &Client{
		codec:   codec,
		pending: make(map[uint64]*Call),
	}
	go client.Receive()
	return client
}

func NewClientWithCodecAndTimeout(codec MyJsonCodec) *Client {
	client := &Client{
		codec:   codec,
		pending: make(map[uint64]*Call),
	}
	go client.Receive()
	return client
}

/*func Dial(network, address string) (*Client, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}*/

func Dial(network, address string, ConnectTimeout ...time.Duration) (*Client, error) {
	if len(ConnectTimeout) != 0 {
		return dialTimeout(network, address, ConnectTimeout[0])
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

func dialTimeout(network, address string, ConnectTimeout time.Duration) (client *Client, err error) {
	conn, err := net.DialTimeout(network, address, ConnectTimeout)
	if err != nil {
		return nil, err
	}
	// close the connection if client is nil
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	ch := make(chan *Client, 10)
	go func() {
		//time.Sleep(time.Second * 2)
		client := NewClientWithTimeout(conn)
		ch <- client
	}()
	if ConnectTimeout == 0 {
		c := <-ch
		return c, err
	}
	select {
	case <-time.After(ConnectTimeout):
		conn.Close()
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", ConnectTimeout)
	case cc := <-ch:
		return cc, err
	}
}

// Go 异步调用函数
func (client *Client) Go(serviceMethod string, args any, reply any, done chan *Call) *Call {
	call := new(Call)
	call.ServiceMethod = serviceMethod
	call.Args = args
	call.Reply = reply
	if done == nil {
		done = make(chan *Call, 10) // 缓冲区大小为10
	} else {
		//如果调用方传递完成 ！= nil，它必须有足够的缓冲区来容纳并发数量
		if cap(done) == 0 {
			log.Panic("rpc: done channel is unbuffered")
		}
	}
	call.Done = done
	/*ch := make(chan *Call, 10)
	go func() {
		client.Send(call)
		ch <- call
	}()
	// 使用time.After来设置超时异常
	select {
	case <-time.After(time.Second * 2):
		return nil, fmt.Errorf("rpc client: send timeout: expect within %s", time.Second*2)
	case c := <-ch:
		return c, nil
	}*/
	client.Send(call)
	return call
}

// Call invokes the named function, waits for it to complete, and returns its error status.
/*func (client *Client) Call(serviceMethod string, args any, reply any) error {
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}*/
// 使用context来处理调用超时
func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := client.Go(serviceMethod, args, reply, make(chan *Call, 1))
	select {
	case <-ctx.Done():
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
}
