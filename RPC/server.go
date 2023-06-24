package RPC

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type methodType struct {
	sync.Mutex                //保护numCalls
	method     reflect.Method //方法本身
	ArgType    reflect.Type   //第一个参数的类型
	ReplyType  reflect.Type   //第二个参数的类型
	numCalls   uint           //被调用次数
}

func (m *methodType) NumCalls() (n uint) {
	m.Lock()
	n = m.numCalls
	m.Unlock()
	return n
}

type service struct {
	name   string                 // 映射的结构体的名称
	rcvr   reflect.Value          // 结构体本身
	typ    reflect.Type           // 结构体的类型
	method map[string]*methodType // 存储映射的结构体的所有符合条件的方法
}

func isExportedOrBuiltinType(typ reflect.Type) bool { //因为注册方法必须为导出或者内置才可以跨包调用
	for typ.Kind() == reflect.Pointer { //跳过指针类型
		typ = typ.Elem()
	}
	//检查是否为导出类型||若typ的包路径是否为空，则表示这是一个内置类型或一个导出类型。
	return isExportedType(typ.Name()) || typ.PkgPath() == ""
}

func isExportedType(name string) bool { //因为注册方法必须为导出或者内置才可以跨包调用
	FirstChar, _ := utf8.DecodeRuneInString(name) //取类型名字首字母
	//若类型名首字母大写字母，则是一个导出类型
	return unicode.IsUpper(FirstChar)
}

// 不带结构体名字注册
func (Server *Server) register(rcvr any) error {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)                  // 反射获取结构体实例
	s.typ = reflect.TypeOf(rcvr)                    // 反射获取结构体的类型
	sname := reflect.Indirect(s.rcvr).Type().Name() //反射获取结构体名字
	if sname == "" {
		s := "rpc.Register: no service name for type " + s.typ.String()
		log.Print(s)
		return errors.New(s)
	}

	if !isExportedType(sname) {
		s := "rpc.Register: type " + sname + " is not exported"
		log.Print(s)
		return errors.New(s)
	}
	s.name = sname

	// 寻找所有符合条件的方法存储到方法map里面方便调用
	s.method = FindAllSuitableMethods(s.typ)
	// 错误处理没有合适的方法
	if len(s.method) == 0 {
		str := "rpc.Register: type " + sname + " has no exported methods of suitable type"
		log.Print(str)
		return errors.New(str)
	}
	// 将server注册到服务器Server中
	if _, no := Server.serviceMap.LoadOrStore(sname, s); no {
		return errors.New("rpc: service already defined: " + sname)
	}
	return nil
}

var typeOfError = reflect.TypeOf((*error)(nil)).Elem() //标记错误反射类型

func FindAllSuitableMethods(typ reflect.Type) map[string]*methodType {
	methods := make(map[string]*methodType) //初始化map储存符合条件方法
	for i := 0; i < typ.NumMethod(); i++ {  //用反射遍历结构体方法
		method := typ.Method(i)
		mtype := method.Type
		mname := method.Name
		// 方法必须是导出的，即方法名的首字母必须是大写字母
		if !method.IsExported() {
			continue
		}
		//输入参数必须有且仅有三个且输出参数必须只有一个
		if mtype.NumIn() != 3 || mtype.NumOut() != 1 {
			continue
		}
		// First arg need not be a pointer.
		argType := mtype.In(1)
		replyType := mtype.In(2)
		// 方法两个输入参数必须是导出类型或内置类型。
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		// 第二个参数必须是是指针类型，地址传递可以存储函数调用的结果
		if replyType.Kind() != reflect.Pointer {
			continue
		}
		// 方法的输出参数必须只有一个，且必须是 error 类型。
		if returnType := mtype.Out(0); returnType != typeOfError {
			continue
		}
		methods[mname] = &methodType{method: method, ArgType: argType, ReplyType: replyType}
	}
	return methods
}

func (server *Server) getHeader() *Header {
	server.hLock.Lock()
	H := server.freeH //线程安全地取出空闲的头部，如果没有的话就new一个
	if H == nil {
		H = new(Header)
	} else {
		server.freeH = H.next
		*H = Header{}
	}
	server.hLock.Unlock()
	return H
}

func (server *Server) freeHeader(h *Header) {
	server.hLock.Lock()
	h.next = server.freeH
	server.freeH = h
	server.hLock.Unlock()
}

type Server struct {
	serviceMap sync.Map   // map[string]*service
	hLock      sync.Mutex // 加锁保护Header链表
	freeH      *Header
}

func NewServer() *Server {
	return &Server{}
}

// 默认Server，方便直接使用
var DefaultServer = NewServer()

// 注册服务方法
func (server *Server) Register(rcvr any) error {
	return server.register(rcvr)
}

// 服务器Server循环接听客户端请求
func (s *Server) Accept(listen net.Listener) {
	s.Register(&Finder{})
	for {
		conn, err := listen.Accept()
		if err != nil {
			log.Println("rpc server: accept error:", err)
			return
		}
		log.Println("Receive request for client:", conn.RemoteAddr())
		go s.ServeConn(conn)
	}
}

// 默认用DefaultServer来Accept，方便使用
func Accept(listen net.Listener) {
	DefaultServer.Accept(listen)
}

func (s *Server) ServeConn(conn net.Conn) {
	serverCodec := NewMyCodec(conn)
	s.ServeCodec(serverCodec)
}

var invalidRequest = struct{}{}

func (server *Server) readRequest(codec MyJsonCodec) (service *service, mtype *methodType, h *Header, argv, replyv reflect.Value, err error) {
	service, mtype, h, err = server.readRequestHeader(codec) //读取请求头部信息
	if err != nil {
		// 假如读取头部发生异常，丢弃body部分，不用继续解码参数
		_ = codec.ReadBody(nil)
		return
	}

	// 解码参数
	argIsValue := false
	if mtype.ArgType.Kind() == reflect.Pointer {
		argv = reflect.New(mtype.ArgType.Elem())
	} else {
		argv = reflect.New(mtype.ArgType)
		argIsValue = true
	}
	// 确保argv是一个指针，ReadBody需要一个指针作为参数
	if err = codec.ReadBody(argv.Interface()); err != nil {
		log.Println("read body err")
		return
	}
	// 如果 mtype.ArgType 是一个值类型，则需要间接引用它并将其存储在指针中
	if argIsValue {
		argv = argv.Elem()
	}
	// 初始化 replyv，使其成为合适类型的 map 或 slice。
	replyv = reflect.New(mtype.ReplyType.Elem())

	switch mtype.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(mtype.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(mtype.ReplyType.Elem(), 0, 0))
	}
	return
}

func (server *Server) readRequestHeader(codec MyJsonCodec) (svc *service, mtype *methodType, h *Header, err error) {
	// 从
	h = server.getHeader()
	err = codec.ReadHeader(h) // 读请求的头部信息，包括seq、methodtype、error
	if err != nil {
		h = nil
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return
		}
		err = errors.New("rpc: server cannot decode request: " + err.Error())
		return
	}

	dot := strings.LastIndex(h.ServiceMethod, ".") // 分割header中的ServiceMethod为serviceName.methodName
	if dot < 0 {
		err = errors.New("rpc: service/method request ill-formed: " + h.ServiceMethod)
		return
	}
	serviceName := h.ServiceMethod[:dot]
	methodName := h.ServiceMethod[dot+1:]

	// 在Server中的serviceMap安全map中查找
	svci, ok := server.serviceMap.Load(serviceName) // 根据serviceName在安全map中找到对应的service
	if !ok {
		err = errors.New("rpc: can't find service " + h.ServiceMethod)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc: can't find method " + h.ServiceMethod)
	}
	return
}

// 处理接收到的具体连接
func (server *Server) ServeCodec(codec MyJsonCodec) {
	sending := new(sync.Mutex)
	wg := new(sync.WaitGroup)
	for {
		service, mtype, h, argv, replyv, err := server.readRequest(codec) //读取请求
		if err != nil {
			if err != io.EOF {
				//if err.Error() == "Read body time out!" {
				//	//err = server.sendResponse(sending, h, invalidRequest, codec, err.Error())
				//}
				log.Println("rpc:", err)
			}
			// 假如发生异常并且req不为空，发送无效请求的响应
			if h != nil {
				err = server.sendResponse(sending, h, invalidRequest, codec, err.Error())
				if err != nil {
					log.Println("rpc:", err.Error())
				}
			}
			if h == nil {
				break
			}
			continue
		}
		// 成功读取请求，然后处理请求
		wg.Add(1) // wg计数加1
		go service.handleRequest(server, sending, wg, mtype, h, argv, replyv, codec)
	}
	// 所有请求请求处理完毕后才关闭编码器
	wg.Wait()
	codec.Close()
}

func (server *Server) sendResponse(sending *sync.Mutex, h *Header, reply any, codec MyJsonCodec, errmsg string) error {

	// 编码响应的头部
	if errmsg != "" {
		h.Error = errmsg
		reply = invalidRequest
	}
	sending.Lock()
	err := codec.Write(h, reply)
	if err != nil {
		log.Println("rpc: writing response:", err)
	}
	sending.Unlock()
	server.freeHeader(h)
	return err
}

func (server *Server) SendResponse(sending *sync.Mutex, h *Header, reply any, codec MyJsonCodec, errmsg string) {

	// 编码响应的头部
	if errmsg != "" {
		h.Error = errmsg
		reply = invalidRequest
	}
	sending.Lock()
	err := codec.Write(h, reply)
	if err != nil {
		log.Println("rpc: writing response:", err)
	}
	sending.Unlock()
	server.freeHeader(h)
}

func (s *service) handleRequest(server *Server, sending *sync.Mutex, wg *sync.WaitGroup, mtype *methodType, h *Header, argv, replyv reflect.Value, codec MyJsonCodec) {
	if wg != nil {
		defer wg.Done()
	}
	mtype.Lock()
	mtype.numCalls++
	mtype.Unlock()
	function := mtype.method.Func
	//ch := make(chan string, 10)
	//go func() {
	//	returnValues := function.Call([]reflect.Value{s.rcvr, argv, replyv}) //利用反射调用注册过的服务的方法
	//	errInter := returnValues[0].Interface()                              //返回参数的[0]对应就是error类型，假如error不为nil那就要处理
	//	ch <- errInter.(error).Error()
	//}()
	//select {
	//case <-time.After(time.Second * 2):
	//	errmsg := ""
	//	errmsg = "write request time out!"
	//	server.sendResponse(sending, h, invalidRequest, codec, errmsg)
	//case errm := <-ch:
	//	errmsg1 := errm
	//	server.sendResponse(sending, h, replyv.Interface(), codec, errmsg1)
	//}
	//returnValues := function.Call([]reflect.Value{s.rcvr, argv, replyv}) //利用反射调用注册过的服务的方法
	//errInter := returnValues[0].Interface()                              //返回参数的[0]对应就是error类型，假如error不为nil那就要处理
	//errmsg := ""
	//if errInter != nil {
	//	errmsg = errInter.(error).Error()
	//}
	//err := server.sendResponse(sending, h, replyv.Interface(), codec, errmsg)
	//if err != nil {
	//	num := 10
	//	for {
	//		num--
	//		err = server.sendResponse(sending, h, replyv.Interface(), codec, errmsg)
	//		if err == nil || num < 0 {
	//			break
	//		}
	//	}
	//}
	called := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		//time.Sleep(time.Second * 15)
		returnValues := function.Call([]reflect.Value{s.rcvr, argv, replyv}) //利用反射调用注册过的服务的方法
		errInter := returnValues[0].Interface()                              //返回参数的[0]对应就是error类型，假如error不为nil那就要处理
		called <- struct{}{}
		errmsg := ""
		if errInter != nil {
			errmsg = errInter.(error).Error()
		}
		err := server.sendResponse(sending, h, replyv.Interface(), codec, errmsg)
		if err != nil {
			log.Println("rpc：", err.Error())
			num := 10
			for {
				num--
				err = server.sendResponse(sending, h, replyv.Interface(), codec, errmsg)
				if err == nil || num < 0 {
					break
				}
			}
		}
		sent <- struct{}{}
	}()
	select {
	case <-time.After(time.Second * 10):
		errmsg := fmt.Sprintf("rpc server: request handle timeout: expect within %s", time.Second*10)
		log.Println(errmsg)
		server.sendResponse(sending, h, invalidRequest, codec, errmsg)
	case <-called:
		<-sent
	}
}

type Finder struct{}

func (f *Finder) GetAllMethod(emptyArg struct{}, methodList *[]string) error {
	// 获取当前服务器支持的所有服务
	services := DefaultServer.GetServiceNames()

	for _, serviceName := range services {
		// 获取每个服务对象
		s, _ := DefaultServer.serviceMap.Load(serviceName)
		svc := s.(*service)
		// 遍历每个服务对象可调用的方法
		for _, m := range svc.method {
			*methodList = append(*methodList, serviceName+"."+m.method.Name+":("+m.ArgType.String()+")\n")
		}
	}

	return nil
}

// GetServiceNames 返回当前 RPC 服务器上所有服务的名称。
func (server *Server) GetServiceNames() []string {
	serviceNames := make([]string, 0)
	server.serviceMap.Range(func(key, value interface{}) bool {
		name, ok := key.(string)
		if !ok {
			return false
		}
		serviceNames = append(serviceNames, name)
		return true
	})
	return serviceNames
}
