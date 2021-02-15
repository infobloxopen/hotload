package fsnotify

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"testing"
	"time"
)

func assertNoError(t *testing.T, name string, err error) {
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func assertStringFromChannel(t *testing.T, name string, want string, from <-chan string) {
	select {
	case got := <-from:
		if got != want {
			t.Fatalf("%s: expected %v got %v", name, want, got)
		}
	case <-time.After(resyncPeriod * 2):
		t.Fatalf("%s: timeout", name)
	}
}

func TestStrategy_Watch(t *testing.T) {

	type args struct {
		pth     string
		options url.Values
	}
	tests := []struct {
		name     string
		setup    func(*args)
		args     args
		wantErr  bool
		post     func(args *args, value string, values <-chan string) error
		tearDown func(*args)
	}{
		{
			name: "file not found",
			args: args{
				pth: "somefile does not exist",
			},
			wantErr: true,
		},
		{
			name: "a, update b",
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
				assertStringFromChannel(t, "wating for update b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		},
		{
			name: "a, rm a, create b",
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
				assertNoError(t, "removing config file", os.Remove(args.pth))
				select {
				case v := <-values:
					return fmt.Errorf("expected no change, got %v", v)
				case <-time.After(time.Second):
				}
				assertNoError(t, "creating new file", ioutil.WriteFile(args.pth, []byte("b"), 0660))
				assertStringFromChannel(t, "wating for create b", "b", values)
				return nil
			},
			tearDown: func(args *args) {
				os.Remove(args.pth)
			},
		},
	}
	s := NewStrategy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(&tt.args)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gotValue, gotValues, err := s.Watch(ctx, tt.args.pth, tt.args.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("Strategy.Watch() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.post != nil {
				if err := tt.post(&tt.args, gotValue, gotValues); err != nil {
					t.Errorf("Strategy.Watch() post: %s", err)
				}
			}
			if tt.tearDown != nil {
				tt.tearDown(&tt.args)
			}
		})
	}
}
