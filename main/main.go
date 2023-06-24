package main

import (
	"RPCTEST/RPC"
	"context"
	"log"
	"net"
	"sync"
	"time"
)

type F int

type A struct{ Num1, Num2 int }

func (f F) Sum(args A, reply *int) error {
	//time.Sleep(time.Second * 10)
	*reply = args.Num1 + args.Num2
	return nil
}

func (f F) Sum1(args A, reply *int) error {
	//time.Sleep(time.Second * 15)
	*reply = args.Num1 + args.Num2
	return nil
}
func startServer() {
	var foo F
	if err := RPC.DefaultServer.Register(&foo); err != nil {
		log.Fatal("register error:", err)
	}
	// pick a free port
	l, err := net.Listen("tcp", "127.0.0.1:9091")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	RPC.Accept(l)
}

func main() {
	log.SetFlags(0)
	go startServer()
	var wg sync.WaitGroup
	for i := 0; i < 11; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client, ERR := RPC.Dial("tcp", "127.0.0.1:9091", 2*time.Second)
			defer func() { _ = client.Close() }()
			args := &A{Num1: i, Num2: i * i}
			var reply int
			ctx, _ := context.WithTimeout(context.Background(), time.Second*10000)
			if ERR == nil {
				if err := client.Call(ctx, "F.Sum", args, &reply); err != nil {
					log.Println("call Foo.Sum error:", err)
				}

				log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
				return
			}
			log.Println("Client nil error")
		}(i)
	}
	wg.Wait()
}
