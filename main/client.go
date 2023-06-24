package main

import (
	"RPCTEST/RPC"
	"context"
	"flag"
	"fmt"
	"log"
	"time"
)

type carg struct {
	Num1, Num2 int
}

func main() {
	// 定义命令行参数
	ip := flag.String("i", "", "Server IP address")
	port := flag.Int("p", 0, "Server port")
	service := flag.String("s", "", "Call Service")
	arg1 := flag.Int("a1", -12345, "arg1")
	arg2 := flag.Int("a2", -12345, "arg2")
	find := flag.Bool("f", false, "Find Service")
	help := flag.Bool("h", false, "Help")
	flag.Parse()

	// 判断是否显示帮助信息
	if *help {
		printHelpC("Client")
		return
	}

	// 检查必要参数是否为空
	if *ip == "" || *port == 0 {
		fmt.Println("Error: IP address and port and Call Service are required")
		printHelpC("Client")
		return
	}

	// 构建服务器地址
	serverAddr := fmt.Sprintf("%s:%d", *ip, *port)
	fmt.Println("")
	fmt.Println("Call Server:", serverAddr)

	// 连接服务器
	client, err := RPC.Dial("tcp", serverAddr)
	if err != nil {
		log.Println("Failed to connect to the server:", err)
		return
	}
	if *find {
		var reply []string
		//fmt.Println("开始call")
		ctx, _ := context.WithTimeout(context.Background(), time.Second*100000)
		if err := client.Call(ctx, "Finder.GetAllMethod", struct{}{}, &reply); err != nil {
			log.Println("call Finder.GetAllMethod error:", err)
		}

		log.Println("Foo.GetAllMethod:")

		fmt.Println("")
		for index, method := range reply {
			fmt.Println("第", index+1, "个可以调用的方法:", method)
		}
		//log.Printf("%s\n", reply)
		client.Close()
		return
	}
	if *service == "" || *arg1 == -12345 || *arg2 == -12345 {
		fmt.Println("Error:  Call Service is required")
		printHelpC("Client")
		return
	}
	// 调用RPC方法
	// ...
	var reply int
	Carg := &carg{
		Num1: *arg1,
		Num2: *arg2,
	}
	log.Println(*service, ":")
	ctx, _ := context.WithTimeout(context.Background(), time.Second*100000)
	if err := client.Call(ctx, *service, Carg, &reply); err != nil {
		log.Println("call ", *service, " error:", err)
	}

	fmt.Printf("Arg 1： %d \nArg 2： %d  \nAnswer：%d", Carg.Num1, Carg.Num2, reply)
	fmt.Println("")
	client.Close()
	return
}

func printHelpC(cmd string) {
	fmt.Printf("Usage: %s -i <server IP address> -p <server port> -s <call service> -a1 <first arg> -a2 <second arg>\n", cmd)
	flag.PrintDefaults()
}
