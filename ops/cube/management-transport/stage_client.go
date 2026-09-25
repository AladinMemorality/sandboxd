// Standalone synthetic transport check; no credentials or Cube operations.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	mode := "smoke"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "bench" {
		benchmark()
		return
	}
	for _, port := range []string{"20300", "20080"} {
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/echo", nil)
		req.Host = "3031-synthetic.cube.test"
		req.Header.Set("X-API-Key", "synthetic-test-only")
		req.Header.Set("Authorization", "Bearer synthetic-test-only")
		resp, err := client.Do(req)
		must(err)
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		must(err)
		if resp.StatusCode != 200 || !strings.Contains(string(data), "3031-synthetic.cube.test") || !strings.Contains(string(data), "Bearer synthetic-test-only") || !strings.Contains(string(data), "synthetic-test-only") {
			panic("header/HTTP mismatch")
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
		must(err)
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "GET /halfclose HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
		must(conn.(*net.TCPConn).CloseWrite())
		all, err := io.ReadAll(conn)
		must(err)
		conn.Close()
		if !strings.HasSuffix(string(all), "late-ok") {
			panic("half-close response lost")
		}
		conn, err = net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
		must(err)
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "GET /upgrade HTTP/1.1\r\nHost: fixture\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		reader := bufio.NewReader(conn)
		resp, err = http.ReadResponse(reader, nil)
		must(err)
		if resp.StatusCode != 101 {
			panic("upgrade failed")
		}
		for _, payload := range []string{"\x81\x04ping", "\x00\xff\x01synthetic"} {
			_, err = conn.Write([]byte(payload))
			must(err)
			b := make([]byte, len(payload))
			_, err = io.ReadFull(reader, b)
			must(err)
			if string(b) != payload {
				panic("duplex mismatch")
			}
		}
		conn.Close()
	}
	fmt.Println(`{"http_headers":true,"upgrade_duplex":true,"delayed_half_close":true,"endpoints":2}`)
}
func benchmark() {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	times := make([]float64, 0, 50)
	for i := 0; i < 50; i++ {
		start := time.Now()
		r, e := client.Get("http://127.0.0.1:20300/healthz")
		must(e)
		b, e := io.ReadAll(io.LimitReader(r.Body, 128))
		r.Body.Close()
		must(e)
		if r.StatusCode != 200 || string(b) != "ready" {
			panic("unhealthy fixture")
		}
		times = append(times, float64(time.Since(start).Microseconds())/1000)
		if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
			time.Sleep(250*time.Millisecond - elapsed)
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"kind": "relay", "samples_ms": times, "count": len(times), "fresh_connections": true})
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
