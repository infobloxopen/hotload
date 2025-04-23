package fsnotify

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func assertStringFromChannel(name string, want string, from <-chan string) {
again:
	select {
	case got := <-from:
		fmt.Printf("assertStringFromChannel: expecting '%s', got update: '%s'\n", want, got)
		if got == want {
			return
		} else {
			// dedup fsnotify WRITE events
			goto again
		}
	case <-time.After(resyncPeriod * 2):
		Fail(fmt.Sprintf("%s: timeout: expecting '%s'", name, want))
	}
}

type args struct {
	pth     string
	options url.Values
}

type test struct {
	name     string
	setup    func(*args)
	args     args
	wantErr  bool
	post     func(args *args, value string, values <-chan string) error
	tearDown func(*args)
}

type testWatcher struct {
	eventChannel chan rfsnotify.Event
	paths        map[string]bool
	closed       bool
	errors       chan error
}

func newTestWatcher() *testWatcher {
	return &testWatcher{
		eventChannel: make(chan rfsnotify.Event),
		paths:        make(map[string]bool),
		errors:       make(chan error),
		closed:       false,
	}
}

func (tw *testWatcher) Add(s string) error {
	tw.paths[s] = true
	return nil
}

func (tw *testWatcher) Remove(s string) error {
	tw.paths[s] = false
	return nil
}

func (tw *testWatcher) Close() error {
	tw.closed = true
	return nil
}

func (tw *testWatcher) GetEvents() <-chan rfsnotify.Event {
	return tw.eventChannel
}

func (tw *testWatcher) GetErrors() <-chan error {
	return tw.errors
}

var _ = Describe("FileWatcher", func() {
	const (
		paramsURL    = "postgres://login:password@host:1234/database?sslmode=disable"
		paramsParsed = "host=a login=b password=c"
	)

	s := NewStrategy()
	DescribeTable("Watch",
		func(tt test) {
			if tt.setup != nil {
				tt.setup(&tt.args)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gotValue, gotValues, err := s.Watch(ctx, tt.args.pth, tt.args.options.Encode())
			if (err != nil) != tt.wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			if tt.post != nil {
				if err := tt.post(&tt.args, gotValue, gotValues); err != nil {
					Expect(err).ToNot(HaveOccurred())
				}
			}
			if tt.tearDown != nil {
				tt.tearDown(&tt.args)
			}

			_ = s.CloseWatch(tt.args.pth, tt.args.options.Encode())
			time.Sleep(10 * time.Millisecond)
		},
		Entry("file not found", test{
			args: args{
				pth: "somefile does not exist",
			},
			wantErr: true,
		}),
		Entry("URL surrounded with whitespaces --> URL trimmed", test{
			setup: func(args *args) {
				f, _ := os.CreateTemp("", "unittest_")
				f.Write([]byte("\r\n \t " + paramsURL + " \t \r\n"))
				args.pth = f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != paramsURL {
					return fmt.Errorf("expected '"+paramsURL+"' got %v", value)
				}
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
		Entry("params surrounded with whitespaces --> params trimmed", test{
			setup: func(args *args) {
				f, _ := os.CreateTemp("", "unittest_")
				f.Write([]byte("\r\n \t " + paramsParsed + " \t \r\n"))
				args.pth = f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != paramsParsed {
					return fmt.Errorf("expected '"+paramsParsed+"' got %v", value)
				}
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
		Entry("a, update b", test{
			setup: func(args *args) {
				f, _ := os.CreateTemp("", "unittest_")
				f.Write([]byte("a"))
				args.pth = f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != "a" {
					return fmt.Errorf("expected 'a' got %v", value)
				}
				os.WriteFile(args.pth, []byte("b"), 0660)
				assertStringFromChannel("waiting for update b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
		Entry("extra slash in path", test{
			setup: func(args *args) {
				f, _ := os.CreateTemp("", "unittest_")
				f.Write([]byte("a"))
				args.pth = "/" + f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != "a" {
					return fmt.Errorf("expected 'a' got %v", value)
				}
				os.WriteFile(args.pth, []byte("b"), 0660)
				assertStringFromChannel("waiting for update b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
		Entry("a, rm a, create b", test{
			setup: func(args *args) {
				f, _ := os.CreateTemp("", "unittest_")
				f.Write([]byte("a"))
				args.pth = f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != "a" {
					return fmt.Errorf("expected 'a' got %v", value)
				}
				err := os.Remove(args.pth)
				Expect(err).ToNot(HaveOccurred(), "removing config file")

				select {
				case v := <-values:
					return fmt.Errorf("expected no change, got %v", v)
				case <-time.After(time.Second):
				}
				err = os.WriteFile(args.pth, []byte("b"), 0660)

				Expect(err).ToNot(HaveOccurred(), "creating new file")

				assertStringFromChannel("waiting for create b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
	)

	Context("run", func() {
		var strat *Strategy
		var watcher *testWatcher
		BeforeEach(func() {
			strat = NewStrategy()
			watcher = newTestWatcher()
			strat.watcher = watcher
		})
		It("Should not respond to chmod events", func() {
			// add only a bad path to the testWatcher
			// This path should not end up removed from the map, ie, marked 'false'
			// we'll pass a CHMOD event and verify the 'bad path' is still in the paths map
			bp := "badpath"
			strat.watcher.Add(bp)
			go s.runLoop()
			go func() {
				watcher.eventChannel <- rfsnotify.Event{
					Name: "chaff",
					Op:   rfsnotify.Chmod,
				}
			}()
			time.Sleep(1 * time.Millisecond)
			_, v := watcher.paths[bp]
			// run didn't pass through resync
			Expect(v).To(BeTrue())
		})
	})

	Context("Two watches with same path but diff query-params", func() {
		It("Should update channels for both watches", func() {
			f, _ := os.CreateTemp("", "hotload_fsnotify_filewatcher_two_watches_unittest_")
			f.Write([]byte("_"))
			watchPath := f.Name()
			f.Close()
			defer os.Remove(watchPath)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			gotValue1, updateChan1, err := s.Watch(ctx, watchPath, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(updateChan1).ToNot(BeNil())
			Expect(gotValue1).To(Equal("_"))

			urlValues := url.Values{"forceKill": []string{"true"}}
			gotValue2, updateChan2, err := s.Watch(ctx, watchPath, urlValues.Encode())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateChan2).ToNot(BeNil())
			Expect(gotValue2).To(Equal("_"))

			os.WriteFile(watchPath, []byte("a"), 0666)
			assertStringFromChannel("updateChan1 waiting for update a", "a", updateChan1)
			assertStringFromChannel("updateChan2 waiting for update a", "a", updateChan2)

			os.WriteFile(watchPath, []byte("b"), 0666)
			assertStringFromChannel("updateChan1 waiting for update b", "b", updateChan1)
			assertStringFromChannel("updateChan2 waiting for update b", "b", updateChan2)

			_ = s.CloseWatch(watchPath, "")
			_ = s.CloseWatch(watchPath, urlValues.Encode())
			time.Sleep(10 * time.Millisecond)
		})
	})
})
