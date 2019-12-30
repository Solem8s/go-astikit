package astikit

import (
	"bytes"
	"context"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestServeHTTP(t *testing.T) {
	w := NewWorker(WorkerOptions{})
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		t.Fatalf("expected no error, got %+v", err)
	}
	ln.Close()
	var i int
	ServeHTTP(w, ServeHTTPOptions{
		Addr: ln.Addr().String(),
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			w.Stop()
			time.Sleep(100*time.Millisecond)
			i++
		}),
	})
	go func() {
		c := &http.Client{}
		r, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String(), nil)
		c.Do(r)
	}()
	w.Wait()
	if e := 1; i != e {
		t.Errorf("expected %+v, got %+v", e, i)
	}
}

type mockedHTTPClient func(req *http.Request) (*http.Response, error)

func (c mockedHTTPClient) Do(req *http.Request) (*http.Response, error) { return c(req) }

type mockedNetError struct{ temporary bool }

func (err mockedNetError) Error() string   { return "" }
func (err mockedNetError) Timeout() bool   { return false }
func (err mockedNetError) Temporary() bool { return err.temporary }

func TestHTTPSender(t *testing.T) {
	// All errors
	var c int
	s := NewHTTPSender(HTTPSenderOptions{
		Client: mockedHTTPClient(func(req *http.Request) (resp *http.Response, err error) {
			c++
			resp = &http.Response{StatusCode: http.StatusInternalServerError}
			return
		}),
		RetryMax: 3,
	})
	_, err := s.Send(&http.Request{})
	if err == nil {
		t.Error("expected error, got nil")
	}
	if e := 4; c != e {
		t.Errorf("expected %v, got %v", e, c)
	}

	// Successful after retries
	c = 0
	s = NewHTTPSender(HTTPSenderOptions{
		Client: mockedHTTPClient(func(req *http.Request) (resp *http.Response, err error) {
			c++
			switch c {
			case 1:
				resp = &http.Response{StatusCode: http.StatusInternalServerError}
			case 2:
				err = mockedNetError{temporary: true}
			default:
				// No retrying
				resp = &http.Response{StatusCode: http.StatusBadRequest}
			}
			return
		}),
		RetryMax: 3,
	})
	_, err = s.Send(&http.Request{})
	if err != nil {
		t.Errorf("expected no error, got %+v", err)
	}
	if e := 3; c != e {
		t.Errorf("expected %v, got %v", e, c)
	}
}

func TestHTTPDownloader(t *testing.T) {
	// Create temp dir
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("creating temp dir failed: %w", err)
	}

	// Make sure to delete temp dir
	defer os.RemoveAll(dir)

	// Create downloader
	d := NewHTTPDownloader(HTTPDownloaderOptions{
		Limiter: GoroutineLimiterOptions{Max: 2},
		Sender: HTTPSenderOptions{
			Client: mockedHTTPClient(func(req *http.Request) (resp *http.Response, err error) {
				// In case of DownloadInWriter we want to check if the order is kept event
				// if downloaded order is messed up
				if req.URL.EscapedPath() == "/path/to/2" {
					time.Sleep(100*time.Millisecond)
				}
				resp = &http.Response{
					Body:       ioutil.NopCloser(bytes.NewBufferString(req.URL.EscapedPath())),
					StatusCode: http.StatusOK,
				}
				return
			}),
		},
	})
	defer d.Close()

	// Download in directory
	err = d.DownloadInDirectory(context.Background(), dir,
		HTTPDownloaderSrc{URL: "/path/to/1"},
		HTTPDownloaderSrc{URL: "/path/to/2"},
		HTTPDownloaderSrc{URL: "/path/to/3"},
	)
	if err != nil {
		t.Errorf("expected no error, got %+v", err)
	}
	dt := make(map[string]string)
	err = filepath.Walk(dir, func(path string, info os.FileInfo, e error) (err error) {
		// Check error
		if e != nil {
			return e
		}

		// Don't process root
		if path == dir {
			return
		}

		// Read
		var b []byte
		if b, err = ioutil.ReadFile(path); err != nil {
			return
		}

		// Add to map
		dt[filepath.Base(path)] = string(b)
		return
	})
	if err != nil {
		t.Errorf("expected no error, got %+v", err)
	}
	if e := map[string]string{
		"1": "/path/to/1",
		"2": "/path/to/2",
		"3": "/path/to/3",
	}; !reflect.DeepEqual(e, dt) {
		t.Errorf("expected %+v, got %+v", e, dt)
	}

	// Download in writer
	w := &bytes.Buffer{}
	err = d.DownloadInWriter(context.Background(), w,
		HTTPDownloaderSrc{URL: "/path/to/1"},
		HTTPDownloaderSrc{URL: "/path/to/2"},
		HTTPDownloaderSrc{URL: "/path/to/3"},
	)
	if err != nil {
		t.Errorf("expected no error, got %+v", err)
	}
	if e, g := "/path/to/1/path/to/2/path/to/3", w.String(); e != g {
		t.Errorf("expected %s, got %s", e, g)
	}

	// Download in file
	p := filepath.Join(dir, "f")
	err = d.DownloadInFile(context.Background(), p,
		HTTPDownloaderSrc{URL: "/path/to/1"},
		HTTPDownloaderSrc{URL: "/path/to/2"},
		HTTPDownloaderSrc{URL: "/path/to/3"},
	)
	if err != nil {
		t.Errorf("expected no error, got %+v", err)
	}
	checkFile(t, p, "/path/to/1/path/to/2/path/to/3")
}
