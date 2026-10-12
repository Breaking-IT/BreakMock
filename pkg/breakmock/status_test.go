package breakmock

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWireStatusReasonsKeepAlive(t *testing.T) {
	for _, journal := range []bool{false, true} {
		t.Run(fmt.Sprint(journal), func(t *testing.T) {
			s, err := NewServer(Options{RootDir: t.TempDir(), DisableJournal: !journal})
			if err != nil {
				t.Fatal(err)
			}
			for _, code := range []int{209, 200, 500, 418} {
				s.Store.AddMapping(Mapping{"request": map[string]any{"url": fmt.Sprintf("/%d", code)}, "response": map[string]any{"status": code, "statusMessage": "ignored by v4 Jetty", "body": "body"}})
			}
			server := httptest.NewUnstartedServer(s)
			server.Listener = WrapWireListener(server.Listener, journal)
			server.Config.ConnContext = HeaderOrderConnContext
			server.Start()
			defer server.Close()
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(conn)
			for _, expected := range []string{"209 209", "200 OK", "500 Server Error", "418 I'm a Teapot"} {
				io.WriteString(conn, fmt.Sprintf("GET /%s HTTP/1.1\r\nHost: example.test\r\n\r\n", expected[:3]))
				response, err := http.ReadResponse(reader, nil)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.Status != expected || string(body) != "body" {
					t.Fatalf("%s body=%q err=%v", response.Status, body, err)
				}
			}
		})
	}
}

func TestStatusLookingBodyIsNeverRewritten(t *testing.T) {
	payload := "HTTP/1.1 500 arbitrary body text\r\nbytes\r\n"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RequestHeaderOrder(r)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		io.WriteString(w, payload)
	}))
	server.Listener = WrapWireListener(server.Listener, false)
	server.Config.ConnContext = HeaderOrderConnContext
	server.Start()
	defer server.Close()
	for i := 0; i < 2; i++ {
		response, err := server.Client().Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != payload {
			t.Fatalf("body=%q error=%v", body, err)
		}
	}
}
