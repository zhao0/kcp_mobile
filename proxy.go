package mobilekcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

// Config 用于接收来自 Java/Kotlin 的配置
type Config struct {
	LocalPort   int    `json:"localport"`   // 本地监听端口 (如 8888)
	RemoteAddr  string `json:"remoteaddr"`  // 服务端地址 (如 1.2.3.4:4000)
	Mode        string `json:"mode"`        // 模式: fast3, fast2, fast, normal
	Conn        int    `json:"conn"`        // UDP 连接数 (默认 1)
	MTU         int    `json:"mtu"`         // MTU (默认 1350)
	SndWnd      int    `json:"sndwnd"`      // 发送窗口 (默认 128)
	RcvWnd      int    `json:"rcvwnd"`      // 接收窗口 (默认 512)
	DataShard   int    `json:"datashard"`   // FEC 数据分片 (默认 10)
	ParityShard int    `json:"parityshard"` // FEC 校验分片 (默认 3)
	AckNodelay  bool   `json:"acknodelay"`  // ACK 无延迟
	SmuxVer     int    `json:"smuxver"`     // SMUX 版本 (1 或 2)
	SmuxBuf     int    `json:"smuxbuf"`     // SMUX 缓冲区大小
	StreamBuf   int    `json:"streambuf"`   // 流缓冲区大小
	KeepAlive   int    `json:"keepalive"`   // 心跳间隔 (秒)

	// 内部参数 (由 Mode 决定，或手动设置)
	NoDelay      int `json:"nodelay"`
	Interval     int `json:"interval"`
	Resend       int `json:"resend"`
	NoCongestion int `json:"nc"`
}

var (
	listener net.Listener
	sessions []*smux.Session
	mu       sync.Mutex
	running  bool
)

// StartProxy 启动代理
// configJson: JSON 格式的配置字符串
func StartProxy(configJson string) string {
	mu.Lock()
	defer mu.Unlock()

	if running {
		return "Proxy already running"
	}

	var cfg Config
	if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
		return "Config Error: " + err.Error()
	}

	// 设置默认值
	applyDefaults(&cfg)

	// 根据模式设置参数
	applyMode(&cfg)

	var err error
	listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.LocalPort))
	if err != nil {
		return "Listen Error: " + err.Error()
	}

	// 预创建 KCP 连接池
	sessions = make([]*smux.Session, cfg.Conn)
	for i := 0; i < cfg.Conn; i++ {
		session, err := createSession(&cfg)
		if err != nil {
			listener.Close()
			return "Session Error: " + err.Error()
		}
		sessions[i] = session
	}

	running = true
	go acceptLoop(&cfg)

	return ""
}

// StopProxy 停止代理
func StopProxy() {
	mu.Lock()
	defer mu.Unlock()

	if !running {
		return
	}

	running = false

	if listener != nil {
		listener.Close()
		listener = nil
	}

	for _, session := range sessions {
		if session != nil {
			session.Close()
		}
	}
	sessions = nil
}

// applyDefaults 设置配置默认值
func applyDefaults(cfg *Config) {
	if cfg.Conn <= 0 {
		cfg.Conn = 1
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1350
	}
	if cfg.SndWnd <= 0 {
		cfg.SndWnd = 128
	}
	if cfg.RcvWnd <= 0 {
		cfg.RcvWnd = 512
	}
	if cfg.DataShard <= 0 {
		cfg.DataShard = 10
	}
	if cfg.ParityShard <= 0 {
		cfg.ParityShard = 3
	}
	if cfg.SmuxVer <= 0 {
		cfg.SmuxVer = 1
	}
	if cfg.SmuxBuf <= 0 {
		cfg.SmuxBuf = 4194304
	}
	if cfg.StreamBuf <= 0 {
		cfg.StreamBuf = 2097152
	}
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = 10
	}
	if cfg.Mode == "" {
		cfg.Mode = "fast"
	}
}

// applyMode 根据模式设置 KCP 参数
func applyMode(cfg *Config) {
	switch cfg.Mode {
	case "normal":
		cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion = 0, 40, 2, 1
	case "fast":
		cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion = 0, 30, 2, 1
	case "fast2":
		cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion = 1, 20, 2, 1
	case "fast3":
		cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion = 1, 10, 2, 1
	default:
		// manual 模式：使用用户指定的参数，或默认 fast
		if cfg.NoDelay == 0 && cfg.Interval == 0 {
			cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion = 0, 30, 2, 1
		}
	}
}

// createSession 创建 KCP+SMUX 会话
func createSession(cfg *Config) (*smux.Session, error) {
	// 建立 KCP 连接 (无加密)
	kcpConn, err := kcp.DialWithOptions(cfg.RemoteAddr, nil, cfg.DataShard, cfg.ParityShard)
	if err != nil {
		return nil, err
	}

	// 设置 KCP 参数
	kcpConn.SetStreamMode(true)
	kcpConn.SetWriteDelay(false)
	kcpConn.SetNoDelay(cfg.NoDelay, cfg.Interval, cfg.Resend, cfg.NoCongestion)
	kcpConn.SetWindowSize(cfg.SndWnd, cfg.RcvWnd)
	kcpConn.SetMtu(cfg.MTU)
	kcpConn.SetACKNoDelay(cfg.AckNodelay)

	// 创建 SMUX 会话 (无压缩)
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = cfg.SmuxVer
	smuxConfig.MaxReceiveBuffer = cfg.SmuxBuf
	smuxConfig.MaxStreamBuffer = cfg.StreamBuf
	smuxConfig.KeepAliveInterval = time.Duration(cfg.KeepAlive) * time.Second

	session, err := smux.Client(kcpConn, smuxConfig)
	if err != nil {
		kcpConn.Close()
		return nil, err
	}

	return session, nil
}

// acceptLoop 接受 TCP 连接的循环
func acceptLoop(cfg *Config) {
	rr := 0 // round-robin 计数器

	for {
		conn, err := listener.Accept()
		if err != nil {
			return // 监听关闭
		}

		// 选择会话 (round-robin)
		idx := rr % len(sessions)
		rr++

		session := sessions[idx]

		// 检查会话是否关闭，尝试重连
		if session == nil || session.IsClosed() {
			newSession, err := createSession(cfg)
			if err != nil {
				conn.Close()
				continue
			}
			mu.Lock()
			sessions[idx] = newSession
			mu.Unlock()
			session = newSession
		}

		go handleConn(conn, session)
	}
}

// handleConn 处理单个连接
func handleConn(p1 net.Conn, session *smux.Session) {
	defer p1.Close()

	// 在 SMUX 会话上打开一个流
	p2, err := session.OpenStream()
	if err != nil {
		return
	}
	defer p2.Close()

	// 双向数据转发
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(p1, p2)
		p1.(*net.TCPConn).CloseRead()
		wg.Done()
	}()

	go func() {
		io.Copy(p2, p1)
		p2.Close()
		wg.Done()
	}()

	wg.Wait()
}
