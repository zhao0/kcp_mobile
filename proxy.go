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

// Config 用于接收来自 Java/Kotlin 的配置
type Config struct {
    LocalPort   int    // 本地监听端口 (如 8888)
    RemoteAddr  string // 服务端地址 (如 1.2.3.4:4000)
    DataShard   int    // 10
    ParityShard int    // 5
}

var listener net.Listener

// StartProxy 启动代理
// configJson: JSON 格式的配置字符串
func StartProxy(configJson string) string {
    var cfg Config
    if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
        return "Config Error: " + err.Error()
    }

    var err error
    listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.LocalPort))
    if err != nil {
        return "Listen Error: " + err.Error()
    }

    go func() {
        for {
            conn, err := listener.Accept()
            if err != nil {
                return // 监听关闭
            }
            go handleConn(conn, cfg)
        }
    }()

    return ""
}

// StopProxy 停止代理
func StopProxy() {
    if listener != nil {
        listener.Close()
    }
}

func handleConn(p1 net.Conn, cfg Config) {
    defer p1.Close()

    // 1. 建立 KCP 连接 (对应服务端的配置)
    // Crypt: none, Mode: fast3 (手动配置参数)
    kcpConn, err := kcp.DialWithOptions(cfg.RemoteAddr, nil, cfg.DataShard, cfg.ParityShard)
    if err != nil {
        return
    }
    defer kcpConn.Close()

    // 2. KCP 参数调优 (对应 fast3 + nocomp)
    kcpConn.SetNoDelay(1, 10, 2, 1)
    kcpConn.SetWindowSize(1024, 1024)
    kcpConn.SetMtu(1350)
    kcpConn.SetACKNoDelay(true)
    kcpConn.SetWriteDelay(false) // 视频流建议设为 false 减少抖动

    // 3. 建立 SMUX 会话 (官方兼容的关键)
    smuxConfig := smux.DefaultConfig()
    smuxConfig.Version = 1
    smuxConfig.KeepAliveInterval = 10 * time.Second

    session, err := smux.Client(kcpConn, smuxConfig)
    if err != nil {
        return
    }
    defer session.Close()

    // 4. 打开流
    p2, err := session.OpenStream()
    if err != nil {
        return
    }
    defer p2.Close()

    // 5. 数据转发
    go io.Copy(p1, p2)
    io.Copy(p2, p1)
}