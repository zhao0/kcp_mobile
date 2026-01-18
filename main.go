// The MIT License (MIT)
//
// # Copyright (c) 2016 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package mobilekcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	kcp "github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

const (
	// maximum supported smux version
	maxSmuxVer = 2
)

// VERSION is injected by buildflags
var VERSION = "MOBILE-1.0"

var (
	proxyListener net.Listener
	proxySessions []*smux.Session
	proxyMu       sync.Mutex
	proxyRunning  bool
	proxyConfig   *Config
	stopChan      chan struct{}
)

// StartProxy 启动代理服务
// configJson: JSON 格式的配置字符串
// 返回空字符串表示成功，否则返回错误信息
func StartProxy(configJson string) string {
	proxyMu.Lock()
	defer proxyMu.Unlock()

	if proxyRunning {
		return "Proxy already running"
	}

	var config Config
	if err := json.Unmarshal([]byte(configJson), &config); err != nil {
		return "Config Error: " + err.Error()
	}

	// 应用默认值
	applyDefaults(&config)

	// 根据模式设置 KCP 参数
	applyMode(&config)

	// 验证配置
	if err := validateConfig(&config); err != nil {
		return "Validate Error: " + err.Error()
	}

	// 启动 TCP 监听
	var err error
	proxyListener, err = net.Listen("tcp", config.LocalAddr)
	if err != nil {
		return "Listen Error: " + err.Error()
	}

	// 预创建 SMUX 会话池
	proxySessions = make([]*smux.Session, config.Conn)
	for i := 0; i < config.Conn; i++ {
		session, err := createSession(&config)
		if err != nil {
			proxyListener.Close()
			return "Session Error: " + err.Error()
		}
		proxySessions[i] = session
	}

	proxyConfig = &config
	proxyRunning = true
	stopChan = make(chan struct{})

	go acceptLoop()

	log.Printf("KCP Proxy started on %s -> %s (mode: %s)", config.LocalAddr, config.RemoteAddr, config.Mode)
	return ""
}

// StopProxy 停止代理服务
func StopProxy() {
	proxyMu.Lock()
	defer proxyMu.Unlock()

	if !proxyRunning {
		return
	}

	proxyRunning = false
	close(stopChan)

	if proxyListener != nil {
		proxyListener.Close()
		proxyListener = nil
	}

	for _, session := range proxySessions {
		if session != nil {
			session.Close()
		}
	}
	proxySessions = nil
	proxyConfig = nil

	log.Println("KCP Proxy stopped")
}

// IsRunning 返回代理是否正在运行
func IsRunning() bool {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	return proxyRunning
}

// GetVersion 返回版本号
func GetVersion() string {
	return VERSION
}

// applyDefaults 设置配置默认值
func applyDefaults(config *Config) {
	if config.LocalAddr == "" {
		config.LocalAddr = "127.0.0.1:1080"
	}
	if config.Conn <= 0 {
		config.Conn = 1
	}
	if config.MTU <= 0 {
		config.MTU = 1350
	}
	if config.SndWnd <= 0 {
		config.SndWnd = 128
	}
	if config.RcvWnd <= 0 {
		config.RcvWnd = 512
	}
	if config.DataShard <= 0 {
		config.DataShard = 10
	}
	if config.ParityShard <= 0 {
		config.ParityShard = 3
	}
	if config.SmuxVer <= 0 {
		config.SmuxVer = 1
	}
	if config.SmuxBuf <= 0 {
		config.SmuxBuf = 4194304
	}
	if config.StreamBuf <= 0 {
		config.StreamBuf = 2097152
	}
	if config.FrameSize <= 0 {
		config.FrameSize = 4096
	}
	if config.KeepAlive <= 0 {
		config.KeepAlive = 10
	}
	if config.SockBuf <= 0 {
		config.SockBuf = 4194304
	}
	if config.Mode == "" {
		config.Mode = "fast"
	}
	// 默认禁用压缩 (NoComp = true)
	config.NoComp = true
}

// applyMode 根据模式设置 KCP 参数
func applyMode(config *Config) {
	switch config.Mode {
	case "normal":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 0, 40, 2, 1
	case "fast":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 0, 30, 2, 1
	case "fast2":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 1, 20, 2, 1
	case "fast3":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 1, 10, 2, 1
	default:
		// 如果模式未知，使用 fast 模式
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 0, 30, 2, 1
	}
}

// validateConfig 验证配置
func validateConfig(config *Config) error {
	if config.RemoteAddr == "" {
		return fmt.Errorf("remoteaddr is required")
	}
	if config.Conn <= 0 {
		return fmt.Errorf("conn must be greater than 0")
	}
	if config.SmuxVer > maxSmuxVer {
		return fmt.Errorf("unsupported smux version: %d", config.SmuxVer)
	}
	return nil
}

// createSession 创建 KCP + SMUX 会话
func createSession(config *Config) (*smux.Session, error) {
	// 建立 KCP 连接 (无加密)
	kcpConn, err := kcp.DialWithOptions(config.RemoteAddr, nil, config.DataShard, config.ParityShard)
	if err != nil {
		return nil, err
	}

	// 设置 KCP 参数
	kcpConn.SetStreamMode(true)
	kcpConn.SetWriteDelay(false)
	kcpConn.SetNoDelay(config.NoDelay, config.Interval, config.Resend, config.NoCongestion)
	kcpConn.SetWindowSize(config.SndWnd, config.RcvWnd)
	kcpConn.SetMtu(config.MTU)
	kcpConn.SetACKNoDelay(config.AckNodelay)

	if err := kcpConn.SetReadBuffer(config.SockBuf); err != nil {
		log.Println("SetReadBuffer:", err)
	}
	if err := kcpConn.SetWriteBuffer(config.SockBuf); err != nil {
		log.Println("SetWriteBuffer:", err)
	}

	// 创建 SMUX 会话 (无压缩)
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = config.SmuxVer
	smuxConfig.MaxReceiveBuffer = config.SmuxBuf
	smuxConfig.MaxStreamBuffer = config.StreamBuf
	smuxConfig.MaxFrameSize = config.FrameSize
	smuxConfig.KeepAliveInterval = time.Duration(config.KeepAlive) * time.Second

	if err := smux.VerifyConfig(smuxConfig); err != nil {
		kcpConn.Close()
		return nil, err
	}

	session, err := smux.Client(kcpConn, smuxConfig)
	if err != nil {
		kcpConn.Close()
		return nil, err
	}

	log.Printf("Session created: %s -> %s", kcpConn.LocalAddr(), kcpConn.RemoteAddr())
	return session, nil
}

// acceptLoop 接受连接的循环
func acceptLoop() {
	rr := 0 // round-robin 计数器

	for {
		select {
		case <-stopChan:
			return
		default:
		}

		conn, err := proxyListener.Accept()
		if err != nil {
			select {
			case <-stopChan:
				return
			default:
				log.Println("Accept error:", err)
				continue
			}
		}

		proxyMu.Lock()
		if !proxyRunning {
			proxyMu.Unlock()
			conn.Close()
			return
		}

		// 选择会话 (round-robin)
		idx := rr % len(proxySessions)
		rr++

		session := proxySessions[idx]

		// 检查会话是否关闭，尝试重连
		if session == nil || session.IsClosed() {
			newSession, err := createSession(proxyConfig)
			if err != nil {
				proxyMu.Unlock()
				log.Println("Reconnect error:", err)
				conn.Close()
				continue
			}
			proxySessions[idx] = newSession
			session = newSession
		}
		proxyMu.Unlock()

		go handleClient(conn, session)
	}
}

// handleClient 处理单个客户端连接
func handleClient(p1 net.Conn, session *smux.Session) {
	defer p1.Close()

	// 在 SMUX 会话上打开一个流
	p2, err := session.OpenStream()
	if err != nil {
		log.Println("OpenStream error:", err)
		return
	}
	defer p2.Close()

	// 双向数据转发
	var wg sync.WaitGroup
	wg.Add(2)

	// p2 -> p1
	go func() {
		defer wg.Done()
		io.Copy(p1, p2)
		if tcpConn, ok := p1.(*net.TCPConn); ok {
			tcpConn.CloseRead()
		}
	}()

	// p1 -> p2
	go func() {
		defer wg.Done()
		io.Copy(p2, p1)
		p2.Close()
	}()

	wg.Wait()
}
