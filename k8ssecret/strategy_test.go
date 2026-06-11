package k8ssecret

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func makeSecret(namespace, name string, data map[string]string) *corev1.Secret {
	bs := make(map[string][]byte, len(data))
	for k, v := range data {
		bs[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       bs,
	}
}

func fakeClientset(t *testing.T, secrets ...*corev1.Secret) kubernetes.Interface {
	t.Helper()
	cs := fake.NewClientset()
	for _, sec := range secrets {
		if _, err := cs.CoreV1().Secrets(sec.Namespace).Create(context.Background(), sec, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return cs
}

// newTestStrategy returns a strategy on a fake clientset with a short
// reconnect backoff, closed when the test ends.
func newTestStrategy(t *testing.T, cs kubernetes.Interface) *Strategy {
	t.Helper()
	s := NewStrategyWithClientset(cs)
	s.backoff = 20 * time.Millisecond
	t.Cleanup(s.Close)
	return s
}

func awaitValue(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("update channel closed while waiting for %q", want)
			}
			t.Logf("update: %q", got)
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for value %q", want)
		}
	}
}

func awaitClosed(t *testing.T, ch <-chan string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel close")
		}
	}
}

func updateSecret(t *testing.T, cs kubernetes.Interface, sec *corev1.Secret) {
	t.Helper()
	if _, err := cs.CoreV1().Secrets(sec.Namespace).Update(context.Background(), sec, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchInitialValue(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "postgres://host/db"}))
	s := newTestStrategy(t, cs)

	// The hotload core passes uri.Path, which has a leading slash.
	val, ch, err := s.Watch(context.Background(), "/mydb", "dsn=dsn&namespace=prod")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if val != "postgres://host/db" {
		t.Errorf("initial value = %q, want postgres://host/db", val)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}
}

func TestWatchSecretNotFound(t *testing.T) {
	s := newTestStrategy(t, fakeClientset(t))
	if _, _, err := s.Watch(context.Background(), "/missing", "namespace=prod"); err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestWatchKeyNotFound(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"password": "hunter2"}))
	s := newTestStrategy(t, cs)
	if _, _, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn"); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestWatchDefaults(t *testing.T) {
	// Default key is dsn.txt; default namespace outside a pod is "default".
	cs := fakeClientset(t, makeSecret("default", "mydb", map[string]string{"dsn.txt": "postgres://host/db"}))
	s := newTestStrategy(t, cs)

	val, _, err := s.Watch(context.Background(), "/mydb", "")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if val != "postgres://host/db" {
		t.Errorf("value = %q, want postgres://host/db", val)
	}
}

func TestWatchSeesUpdates(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := newTestStrategy(t, cs)

	_, ch, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}

	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}))
	awaitValue(t, ch, "dsn-2")

	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-3"}))
	awaitValue(t, ch, "dsn-3")
}

// TestWatchSeesDeleteAndRecreate: a deleted Secret keeps serving the last
// value; the recreate arrives as an Added event and propagates.
func TestWatchSeesDeleteAndRecreate(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := newTestStrategy(t, cs)

	_, ch, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}

	if err := cs.CoreV1().Secrets("prod").Delete(context.Background(), "mydb", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Secrets("prod").Create(context.Background(), makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	awaitValue(t, ch, "dsn-2")
}

// TestWatchCatchesUpAfterReconnect: a change made while the API watch is
// down must be delivered by the reconnect's catch-up read — the gap that
// loses updates when reconnects only resume watching from "now".
func TestWatchCatchesUpAfterReconnect(t *testing.T) {
	cs := fake.NewClientset()
	if _, err := cs.CoreV1().Secrets("prod").Create(context.Background(), makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Hand the strategy a controllable watcher, then kill it.
	firstWatch := watch.NewFake()
	watchCalls := make(chan struct{}, 10)
	first := true
	cs.PrependWatchReactor("secrets", func(action k8stesting.Action) (bool, watch.Interface, error) {
		watchCalls <- struct{}{}
		if first {
			first = false
			return true, firstWatch, nil
		}
		return false, nil, nil // fall through to the default tracker watch
	})

	s := newTestStrategy(t, cs)
	_, ch, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}
	<-watchCalls // first watch established

	// Change the secret while the (about to die) first watch sees nothing,
	// then drop the watch.
	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}))
	firstWatch.Stop()

	// The reconnect's catch-up Get must deliver the missed value.
	awaitValue(t, ch, "dsn-2")
	<-watchCalls // and a second watch was established
}

// TestSlowSubscriberConvergesOnLatest: when a subscriber is not draining
// its channel, intermediate values may drop but the latest must win.
func TestSlowSubscriberConvergesOnLatest(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-0"}))
	s := newTestStrategy(t, cs)

	_, ch, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}

	// Push several updates without reading; the channel has capacity 1.
	for i := 1; i <= 5; i++ {
		updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-x"}))
		updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-final"}))
	}
	// Now drain: the last value seen must be dsn-final, not a stale one
	// stuck in the buffer.
	awaitValue(t, ch, "dsn-final")
}

func TestMultipleSubscribersOneSecret(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := newTestStrategy(t, cs)

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=dsn&forceKill=true")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}

	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}))
	awaitValue(t, ch1, "dsn-2")
	awaitValue(t, ch2, "dsn-2")
}

// TestDistinctKeysAreIndependent: two watchers reading different data keys
// of the same Secret must each get their own key's value.
func TestDistinctKeysAreIndependent(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{
		"primary": "dsn-primary-1",
		"replica": "dsn-replica-1",
	}))
	s := newTestStrategy(t, cs)

	ctx := context.Background()
	vp, chP, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=primary")
	if err != nil {
		t.Fatal(err)
	}
	vr, chR, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=replica")
	if err != nil {
		t.Fatal(err)
	}
	if vp != "dsn-primary-1" || vr != "dsn-replica-1" {
		t.Fatalf("initial values = %q / %q, want dsn-primary-1 / dsn-replica-1", vp, vr)
	}

	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{
		"primary": "dsn-primary-2",
		"replica": "dsn-replica-2",
	}))
	awaitValue(t, chP, "dsn-primary-2")
	awaitValue(t, chR, "dsn-replica-2")
}

func TestCloseWatch(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := newTestStrategy(t, cs)

	ctx := context.Background()
	qry := "namespace=prod&dsn=dsn"
	_, ch, err := s.Watch(ctx, "/mydb", qry)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWatch("/mydb", qry); err != nil {
		t.Fatal(err)
	}
	awaitClosed(t, ch)

	// Closing an unknown watch is a no-op.
	if err := s.CloseWatch("/other", qry); err != nil {
		t.Fatal(err)
	}
}

// TestCloseWatchKeepsOtherSubscribers: closing one subscriber leaves the
// other one live.
func TestCloseWatchKeepsOtherSubscribers(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := newTestStrategy(t, cs)

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=dsn&forceKill=true")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CloseWatch("/mydb", "namespace=prod&dsn=dsn&forceKill=true"); err != nil {
		t.Fatal(err)
	}
	awaitClosed(t, ch1)

	updateSecret(t, cs, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}))
	awaitValue(t, ch2, "dsn-2")
}

func TestStrategyClose(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))
	s := NewStrategyWithClientset(cs)
	s.backoff = 20 * time.Millisecond

	_, ch, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	awaitClosed(t, ch)

	// Watch after Close re-initializes (matches the fsnotify strategy and
	// keeps the package-registered instance usable after UnregisterStrategy
	// + re-register cycles).
	val, _, err := s.Watch(context.Background(), "/mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("Watch after Close: %v", err)
	}
	if val != "dsn-1" {
		t.Errorf("value after reopen = %q, want dsn-1", val)
	}
	s.Close()
}

// TestSecretName covers the path normalization between the hotload DSN and
// the Kubernetes Secret name.
func TestSecretName(t *testing.T) {
	cases := map[string]string{
		"/mydb":   "mydb",
		"mydb":    "mydb",
		" /mydb ": "mydb",
		"/a/b":    "a/b", // invalid as a Secret name; surfaces as a Get error
	}
	for in, want := range cases {
		if got := secretName(in); got != want {
			t.Errorf("secretName(%q) = %q, want %q", in, got, want)
		}
	}
}

var _ runtime.Object = (*corev1.Secret)(nil)
