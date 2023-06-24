package main

import (
	"RPCTEST/RPC"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
)

func main() {
	// 定义命令行参数
	ip := flag.String("l", "0.0.0.0", "Server IP address")
	port := flag.Int("p", 0, "Server port")
	help := flag.Bool("h", false, "Help")
	flag.Parse()

	// 判断是否显示帮助信息
	if *help {
		printHelpS("Server")
		return
	}

	// 检查必要参数是否为空
	if *port == 0 {
		fmt.Println("Error:  port are required")
		printHelpS("Server")
		return
	}

	// 构建监听地址
	listenAddr := fmt.Sprintf("%s:%d", *ip, *port)
	//fmt.Println(listenAddr)

	StartServer(listenAddr)
}

func StartServer(address string) {
	var foo Foo
	if err := RPC.DefaultServer.Register(&foo); err != nil {
		log.Fatal("register error:", err)
	}
	// pick a free port
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", address)
	RPC.Accept(l)
}

func printHelpS(cmd string) {
	fmt.Printf("Usage: %s -l <listen IP address> -p <listen port>\n", cmd)
	flag.PrintDefaults()
}

type Foo int

type Args struct{ Num1, Num2 int }

func (f Foo) Sum(args Args, reply *int) error {
	//time.Sleep(time.Second * 10)
	*reply = args.Num1 + args.Num2
	return nil
}

func (f Foo) Difference(args Args, reply *int) error {
	*reply = args.Num1 - args.Num2
	return nil
}

func (f Foo) Product(args Args, reply *int) error {
	*reply = args.Num1 * args.Num2
	return nil
}

func (f Foo) Quotient(args Args, reply *int) error {
	if args.Num2 == 0 {
		return errors.New("division by zero")
	}
	*reply = args.Num1 / args.Num2
	return nil
}

func (f Foo) Remainder(args Args, reply *int) error {
	*reply = args.Num1 % args.Num2
	return nil
}

func (f Foo) Power(args Args, reply *int) error {
	*reply = int(math.Pow(float64(args.Num1), float64(args.Num2)))
	return nil
}

func (f Foo) SquareRootByInt(args Args, reply *int) error {
	if args.Num1 < 0 {
		return errors.New("cannot calculate square root of a negative number")
	}
	*reply = int(math.Sqrt(float64(args.Num1)))
	return nil
}

func (f Foo) Max(args Args, reply *int) error {
	if args.Num1 > args.Num2 {
		*reply = args.Num1
	} else {
		*reply = args.Num2
	}
	return nil
}

func (f Foo) Min(args Args, reply *int) error {
	if args.Num1 < args.Num2 {
		*reply = args.Num1
	} else {
		*reply = args.Num2
	}
	return nil
}

func (f Foo) MultiplySumByTwo(args Args, reply *int) error {
	*reply = (args.Num1 + args.Num2) * 2
	return nil
}
