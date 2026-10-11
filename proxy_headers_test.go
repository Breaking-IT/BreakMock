package breakmock

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

func TestProxyPreservesHeaderOrderAcrossPooledRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for i := 0; i < 2; i++ {
			req, e := http.ReadRequest(r)
			if e != nil {
				return
			}
			io.Copy(io.Discard, req.Body)
			req.Body.Close()
			fmt.Fprint(c, "HTTP/1.1 103 Early Hints\r\nLink: </style.css>\r\n\r\nHTTP/1.1 200 OK\r\nZ-First: one\r\na-Second: two\r\nZ-First: three\r\nContent-Length: 2\r\n\r\nok")
		}
	}()
	tr := newProxyTransport().(*proxyOrderTransport)
	defer tr.base.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	for i := 0; i < 2; i++ {
		res, e := client.Get("http://" + ln.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		want := []string{"Z-First", "a-Second", "Z-First", "Content-Length"}
		if got := proxyResponseHeaderOrder(res); !reflect.DeepEqual(got, want) {
			t.Fatalf("order=%v want%v", got, want)
		}
	}
	wg.Wait()
}
