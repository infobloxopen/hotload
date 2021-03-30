package fsnotify

import (
	"context"
	"fmt"
	rfsnotify "github.com/fsnotify/fsnotify"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"io/ioutil"
	"net/url"
	"os"
	"time"
)

func assertStringFromChannel(name string, want string, from <-chan string) {
	select {
	case got := <-from:
		if got != want {
			Fail(fmt.Sprintf("%s: expected %v got %v", name, want, got))
		}
	case <-time.After(resyncPeriod * 2):
		Fail(fmt.Sprintf("%s: timeout", name))
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
	s := NewStrategy()
	DescribeTable("Watch",
		func(tt test) {
			if tt.setup != nil {
				tt.setup(&tt.args)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gotValue, gotValues, err := s.Watch(ctx, tt.args.pth, tt.args.options)
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
		},
		Entry("file not found", test{
			args: args{
				pth: "somefile does not exist",
			},
			wantErr: true,
		}),
		Entry("a, update b", test{
			setup: func(args *args) {
				f, _ := ioutil.TempFile("", "unittest_")
				f.Write([]byte("a"))
				args.pth = f.Name()
				f.Close()
			},
			wantErr: false,
			post: func(args *args, value string, values <-chan string) error {
				if value != "a" {
					return fmt.Errorf("expected 'a' got %v", value)
				}
				ioutil.WriteFile(args.pth, []byte("b"), 0660)
				assertStringFromChannel("wating for update b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		}),
		Entry("a, rm a, create b", test{
			setup: func(args *args) {
				f, _ := ioutil.TempFile("", "unittest_")
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
				err = ioutil.WriteFile(args.pth, []byte("b"), 0660)

				Expect(err).ToNot(HaveOccurred(), "creating new file")

				assertStringFromChannel("wating for create b", "b", values)
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
			go s.run()
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
})
