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

type Config struct {
	LocalAddr   string
	RemoteAddr  string
	Mode        string
	MTU         int
	SndWnd      int
	RcvWnd      int
	DataShard   int
	ParityShard int
	DSCP        int
}

var listener net.Listener

func Start(configJson string) string {
	var config Config
	if err := json.Unmarshal([]byte(configJson), &config); err != nil {
		return fmt.Sprintf("Config Error: %v", err)
	}

	var err error
	listener, err = net.Listen("tcp", config.LocalAddr)
	if err != nil {
		return fmt.Sprintf("Listen Error: %v", err)
	}

	fmt.Printf("KCP Started: %s -> %s\n", config.LocalAddr, config.RemoteAddr)

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

	// KCP Dial
	kcpConn, err := kcp.DialWithOptions(config.RemoteAddr, nil, config.DataShard, config.ParityShard)
	if err != nil {
		fmt.Printf("Dial Error: %v\n", err)
		return
	}
	defer kcpConn.Close()

	// KCP Config
	kcpConn.SetNoDelay(1, 10, 2, 1)
	kcpConn.SetWindowSize(config.SndWnd, config.RcvWnd)
	kcpConn.SetMtu(config.MTU)
	kcpConn.SetACKNoDelay(true)
	kcpConn.SetWriteDelay(false)
	if config.DSCP > 0 {
		kcpConn.SetDSCP(config.DSCP)
	}

	// Smux
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = 1
	smuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := smux.Client(kcpConn, smuxConfig)
	if err != nil {
		fmt.Printf("Smux Error: %v\n", err)
		return
	}
	defer session.Close()

	p2, err := session.OpenStream()
	if err != nil {
		fmt.Printf("Stream Error: %v\n", err)
		return
	}
	defer p2.Close()

	// Copy Loop
	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(p1, p2)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(p2, p1)
		errChan <- err
	}()

	<-errChan
}