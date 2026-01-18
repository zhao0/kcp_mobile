package mobilekcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

// 设置 Log 前缀，方便你在 Logcat 里搜 "KCP_DEBUG"
const LogPrefix = "[KCP_DEBUG] "

type Config struct {
	LocalPort  int
	RemoteAddr string
}

func StartProxy(configJson string) string {
	log.Println(LogPrefix + "正在启动代理, 配置: " + configJson)

	var cfg Config
	if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
		errStr := fmt.Sprintf("JSON解析错误: %v", err)
		log.Println(LogPrefix + errStr)
		return errStr
	}

	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.LocalPort))
	if err != nil {
		errStr := fmt.Sprintf("本地端口监听失败: %v", err)
		log.Println(LogPrefix + errStr)
		return errStr
	}

	log.Printf("%s本地 TCP 监听成功: 127.0.0.1:%d -> 目标: %s\n", LogPrefix, cfg.LocalPort, cfg.RemoteAddr)

	go func() {
		for {
			p1, err := l.Accept()
			if err != nil {
				log.Println(LogPrefix + "Listener Accept 错误或服务停止")
				return
			}
			// 给每个连接一个 ID，方便追踪
			connID := p1.RemoteAddr().String()
			log.Printf("%s[ID:%s] 收到播放器请求\n", LogPrefix, connID)
			
			go handleConn(p1, cfg.RemoteAddr, connID)
		}
	}()

	return ""
}

func handleConn(p1 net.Conn, remoteAddr string, connID string) {
	defer p1.Close()

	// ----------------------------------------------------------------
	// 1. KCP 拨号
	// ----------------------------------------------------------------
	log.Printf("%s[ID:%s] 步骤1: 开始拨号 KCP -> %s\n", LogPrefix, connID, remoteAddr)
	
	// 这里必须对应服务端: --crypt none, --datashard 10, --parityshard 5
	kcpConn, err := kcp.DialWithOptions(remoteAddr, nil, 10, 5)
	if err != nil {
		log.Printf("%s[ID:%s] [严重错误] KCP 拨号失败: %v (请检查IP端口/UDP防火墙)\n", LogPrefix, connID, err)
		return
	}
	defer kcpConn.Close()

	// ----------------------------------------------------------------
	// 2. KCP 参数配置
	// ----------------------------------------------------------------
	// 对应服务端: --mode fast3 --mtu 1350 --sndwnd 1024 --rcvwnd 1024 --dscp 46
	kcpConn.SetNoDelay(1, 10, 2, 1)
	kcpConn.SetWindowSize(1024, 1024)
	kcpConn.SetMtu(1350)
	kcpConn.SetDSCP(46)
	kcpConn.SetACKNoDelay(true)
	kcpConn.SetWriteDelay(false) 

	log.Printf("%s[ID:%s] 步骤2: KCP 连接已建立，参数已配置\n", LogPrefix, connID)

	// ----------------------------------------------------------------
	// 3. Smux 握手 (最容易失败的一步)
	// ----------------------------------------------------------------
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = 1
	smuxConfig.KeepAliveInterval = 10 * time.Second

	log.Printf("%s[ID:%s] 步骤3: 开始 Smux 握手 (如果卡在这里，说明服务端没开Smux或者被拦截)\n", LogPrefix, connID)
	
	session, err := smux.Client(kcpConn, smuxConfig)
	if err != nil {
		log.Printf("%s[ID:%s] [严重错误] Smux Client 创建失败: %v\n", LogPrefix, connID, err)
		return
	}
	defer session.Close()

	p2, err := session.OpenStream()
	if err != nil {
		log.Printf("%s[ID:%s] [严重错误] Smux 打开流失败: %v\n", LogPrefix, connID, err)
		return
	}
	defer p2.Close()

	log.Printf("%s[ID:%s] 步骤4: Smux 流打开成功，隧道打通！准备传输数据...\n", LogPrefix, connID)

	// ----------------------------------------------------------------
	// 4. 数据传输与统计
	// ----------------------------------------------------------------
	errChan := make(chan error, 2)

	// 统计流量 (可选，帮助排查是否有数据流动)
	go func() {
		n, err := io.Copy(p1, p2) // 从 KCP 下载
		log.Printf("%s[ID:%s] KCP -> 播放器: 传输结束, 共下载 %d bytes, 错误: %v\n", LogPrefix, connID, n, err)
		errChan <- err
	}()

	go func() {
		n, err := io.Copy(p2, p1) // 上传给 KCP
		log.Printf("%s[ID:%s] 播放器 -> KCP: 传输结束, 共上传 %d bytes, 错误: %v\n", LogPrefix, connID, n, err)
		errChan <- err
	}()

	<-errChan
	log.Printf("%s[ID:%s] 连接关闭\n", LogPrefix, connID)
}
