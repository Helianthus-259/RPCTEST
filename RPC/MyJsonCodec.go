package RPC

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"time"
)

type Header struct {
	ServiceMethod string  // client请求调用的方法，格式: "Service.Method"
	Seq           uint64  // client发送的call的seq
	Error         string  //没有可为空
	next          *Header // 构造header链表，在高并发情形下可以避免new太多header
}

type MyJsonCodec struct {
	rwc    io.ReadWriteCloser // 读写接口
	dec    *json.Decoder      // JSON 解码器
	enc    *json.Encoder      // JSON 编码器
	encBuf *bufio.Writer      // 缓冲写入器
}

var DefaultMyCodec = (*MyJsonCodec)(nil)

func NewMyCodec(conn io.ReadWriteCloser) MyJsonCodec {
	buf := bufio.NewWriter(conn) // 创建缓冲写入器
	return MyJsonCodec{
		rwc:    conn,
		dec:    json.NewDecoder(conn), // 创建 JSON 解码器
		enc:    json.NewEncoder(conn), // 创建 JSON 编码器
		encBuf: buf,
	}
}

func (c *MyJsonCodec) ReadHeader(r *Header) error {
	//return c.dec.Decode(r) // 解码 Header
	ch := make(chan error, 10)
	go func() {
		ch <- c.dec.Decode(r) // 解码 Header
	}()
	select {
	case <-time.After(time.Second * 2):
		return errors.New("读取数据超时！")
	case err := <-ch:
		return err
	}
}

func (c *MyJsonCodec) ReadBody(body any) error {
	ch := make(chan error, 10)
	go func() {
		ch <- c.dec.Decode(body) // 解码 Body
	}()
	select {
	case <-time.After(time.Second * 2):
		return errors.New("读取数据超时！")
	case err := <-ch:
		return err
	}
}

func (c *MyJsonCodec) Write(r *Header, body any) (err error) {
	ch := make(chan error, 10)
	go func() {
		if err = c.enc.Encode(r); err != nil {
			if c.encBuf.Flush() == nil {
				// JSON 无法编码 Header。这不应该发生所以如果发生了，关闭连接以表示连接已断开。
				log.Println("rpc: JSON 编码响应错误：", err)
				c.Close()
			}
			return
		}
		if err = c.enc.Encode(body); err != nil {
			if c.encBuf.Flush() == nil {
				// JSON 无法编码 Body，但 Header 已经被写入。关闭连接以表示连接已断开。
				log.Println("rpc: JSON 编码 Body 错误：", err)
				c.Close()
			}
			return
		}
		ch <- c.encBuf.Flush() // 刷新缓冲写入器
	}()
	select {
	case <-time.After(time.Second * 2):
		return errors.New("写入请求超时！")
	case err := <-ch:
		return err
	}
}

func (c *MyJsonCodec) Close() error {
	return c.rwc.Close() // 关闭读写接口
}
