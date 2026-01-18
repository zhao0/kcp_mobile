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

// Config 客户端配置
// 通过 JSON 传入，支持与 kcptun 服务端匹配的配置
type Config struct {
	// 必填参数
	LocalAddr  string `json:"localaddr"`  // 本地监听地址 (如 "127.0.0.1:1080")
	RemoteAddr string `json:"remoteaddr"` // 远程服务器地址 (如 "1.2.3.4:4000")

	// 模式参数
	Mode string `json:"mode"` // 模式: fast3, fast2, fast, normal (默认 fast)

	// 连接参数
	Conn int `json:"conn"` // UDP 连接数量 (默认 1)

	// KCP 参数
	MTU         int  `json:"mtu"`         // MTU 大小 (默认 1350)
	SndWnd      int  `json:"sndwnd"`      // 发送窗口大小 (默认 128)
	RcvWnd      int  `json:"rcvwnd"`      // 接收窗口大小 (默认 512)
	DataShard   int  `json:"datashard"`   // FEC 数据分片 (默认 10)
	ParityShard int  `json:"parityshard"` // FEC 校验分片 (默认 3)
	AckNodelay  bool `json:"acknodelay"`  // ACK 无延迟 (默认 false)
	SockBuf     int  `json:"sockbuf"`     // Socket 缓冲区 (默认 4194304)

	// SMUX 参数
	SmuxVer   int `json:"smuxver"`   // SMUX 版本 1 或 2 (默认 1)
	SmuxBuf   int `json:"smuxbuf"`   // SMUX 缓冲区 (默认 4194304)
	FrameSize int `json:"framesize"` // 帧大小 (默认 4096)
	StreamBuf int `json:"streambuf"` // 流缓冲区 (默认 2097152)
	KeepAlive int `json:"keepalive"` // 心跳间隔秒数 (默认 10)

	// 内部参数 (由 Mode 决定)
	NoDelay      int  `json:"-"`
	Interval     int  `json:"-"`
	Resend       int  `json:"-"`
	NoCongestion int  `json:"-"`
	NoComp       bool `json:"-"` // 始终为 true，不支持压缩
}
