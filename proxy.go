package mobilekcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

// Config: 仅保留核心网络参数
type Config struct {
	LocalAddr   string // "127.0.0.1:8888"
	RemoteAddr  string // "1.2.3.4:4000"
	Mode        string // "fast3"
	MTU         int    // 1350
	SndWnd      int    // 1024
	RcvWnd      int    // 1024
	DataShard   int    // 10
	ParityShard int    // 5
	DSCP        int    // 46
}

var listener net.Listener

// Start 启动代理
func Start(configJson string) string {
	var config Config
	if err := json.Unmarshal([]byte(configJson), &config); err != nil {
		return fmt.Sprintf("Config JSON Error: %v", err)
	}

	// 1. 监听本地 TCP
	var err error
	listener, err = net.Listen("tcp", config.LocalAddr)
	if err != nil {
		return fmt.Sprintf("Listen Error: %v", err)
	}

	// 使用 fmt 打印启动日志
	fmt.Printf("KCP Proxy Started: %s -> %s\n", config.LocalAddr, config.RemoteAddr)

	go func() {
		for {
			p1, err := listener.Accept()
			if err != nil {
				return
			}
			go handleClient(p1, config)
		}
	}()

	return ""
}

func Stop() {
	if listener != nil {
		listener.Close()
	}
}

func handleClient(p1 net.Conn, config Config) {
	defer p1.Close()

	// 2. KCP 拨号
	// 关键点：第二个参数传 nil，表示无加密 (--crypt none)
	kcpConn, err := kcp.DialWithOptions(config.RemoteAddr, nil, config.DataShard, config.ParityShard)
	if err != nil {
		fmt.Println("Dial Error:", err)
		return
	}
	defer kcpConn.Close()

	// 3. 参数调优
	kcpConn.SetNoDelay(1, 10, 2, 1) // fast3
	kcpConn.SetWindowSize(config.SndWnd, config.RcvWnd)
	kcpConn.SetMtu(config.MTU)
	kcpConn.SetACKNoDelay(true)
	kcpConn.SetWriteDelay(false) 

	if config.DSCP > 0 {
		kcpConn.SetDSCP(config.DSCP)
	}

	// 4. Smux 多路复用
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = 2
	smuxConfig.KeepAliveInterval = 10 * time.Second

	// 直接把 kcpConn 传进去，不套压缩层 (--nocomp)
	session, err := smux.Client(kcpConn, smuxConfig)
	if err != nil {
		fmt.Println("Smux Error:", err)
		return
	}
	defer session.Close()

	p2, err := session.OpenStream()
	if err != nil {
		fmt.Println("Stream Error:", err)
		return
	}
	defer p2.Close()

	// 5. 纯管道转发
	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(p1, p2
