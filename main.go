package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/elitah/utils/exepath"
	"github.com/elitah/utils/logs"
)

var (
	pool = sync.Pool{
		New: func() interface{} {
			// 1k
			return make([]byte, 1024)
		},
	}
)

func getBuffer() []byte {
	if buffer, ok := pool.Get().([]byte); ok {
		return buffer
	}
	return nil
}

func putBuffer(buffer []byte) {
	pool.Put(buffer)
}

func main() {
	var help bool

	var listenAddr string
	var dohAddr string

	var noVerify bool
	var rootCA string

	var timeout int

	var logfile string

	flag.BoolVar(&help, "h", false, "This help")

	flag.StringVar(&listenAddr, "l", ":53", "Your local listen address")
	flag.StringVar(&dohAddr, "s", "https://dns.google/dns-query", "Your DNS service provider for DNS over HTTPS")

	flag.BoolVar(&noVerify, "n", false, "No certificate verify")
	flag.StringVar(&rootCA, "c", exepath.GetExeDir()+"/rootCA.bin", "Your CA file path")

	flag.IntVar(&timeout, "t", 3, "Request timeout for DNS over HTTPS")

	flag.StringVar(&logfile, "logfile", "", "Your logger file")

	flag.Parse()

	if help || "" == listenAddr || "" == dohAddr {
		flag.Usage()
		return
	}

	if 1 > timeout {
		timeout = 3
	}

	if "" != logfile {
		logs.SetLogger(logs.AdapterFile, fmt.Sprintf(`{"level":99,"filename":"%s","maxsize":1048576}`, logfile))
	} else {
		logs.SetLogger(logs.AdapterConsole, `{"level":99,"color":true}`)
	}

	logs.EnableFuncCallDepth(true)
	logs.SetLogFuncCallDepth(3)
	logs.Async()

	defer logs.Close()

	if addr, err := net.ResolveUDPAddr("udp", listenAddr); nil == err {
		if conn, err := net.ListenUDP("udp", addr); nil == err {
			//
			tlsConfig := &tls.Config{
				InsecureSkipVerify: noVerify,
			}
			//
			if "" != rootCA {
				if info, err := os.Stat(rootCA); nil == err {
					if 0 < info.Size() {
						if data, err := ioutil.ReadFile(rootCA); nil == err {
							if pool := x509.NewCertPool(); pool.AppendCertsFromPEM(data) {
								tlsConfig.RootCAs = pool
								tlsConfig.InsecureSkipVerify = false
							}
						} else {
							logs.Warn(err)
						}
					}
				} else {
					//logs.Warn(err)
				}
			}
			//
			client := http.Client{
				Transport: &http.Transport{
					Proxy: nil,
					DialContext: (&net.Dialer{
						Timeout:   30 * time.Second,
						KeepAlive: 30 * time.Second,
						DualStack: true,
					}).DialContext,
					TLSClientConfig:       tlsConfig,
					TLSHandshakeTimeout:   8 * time.Second,
					MaxIdleConns:          32,
					IdleConnTimeout:       30 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
					ForceAttemptHTTP2:     true,
				},
				Timeout: time.Duration(timeout) * time.Second,
			}
			// 信号监控
			go func() {
				sig := make(chan os.Signal, 1)

				signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

				if s, ok := <-sig; ok {
					if ok {
						// 显示信号
						logs.Info("exit by signal: %v", s)
					}
					// 关闭服务
					conn.Close()
				}
			}()
			// 循环接收
			for {
				if buffer := getBuffer(); nil != buffer {
					if n, remote, err := conn.ReadFromUDP(buffer); nil == err {
						if 0 < n {
							go func(d *net.UDPAddr, data []byte, length int) {
								//
								defer putBuffer(data)
								//
								if resp, err := client.Get(
									fmt.Sprintf(
										"%s?dns=%s",
										dohAddr,
										base64.RawURLEncoding.EncodeToString(data[:length]),
									),
								); nil == err {
									if http.StatusOK == resp.StatusCode {
										//
										defer resp.Body.Close()
										//
										n, err := resp.Body.Read(data)
										//
										if 0 < n {
											conn.WriteToUDP(data[:n], d)
										} else if nil != err {
											logs.Warn(err)
										}
									} else {
										logs.Warn("http response code: %d", resp.StatusCode)
									}
								} else {
									logs.Warn(err)
								}
							}(remote, buffer, n)
						} else {
							logs.Warn("short read")
						}
					} else {
						logs.Warn(err)
						// 跳出
						break
					}
					putBuffer(buffer)
				}
			}
		} else {
			logs.Error(err)
		}
	} else {
		logs.Error(err)
	}
}
