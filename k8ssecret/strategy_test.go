package k8ssecret

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeClientset(secrets ...*corev1.Secret) kubernetes.Interface {
	objs := make([]interface{}, len(secrets))
	for i, s := range secrets {
		// Ensure runtime.Object interface satisfied
		_ = s
		objs[i] = s
	}
	cs := fake.NewSimpleClientset()
	for _, s := range secrets {
		cs.CoreV1().Secrets(s.Namespace).Create(context.Background(), s, metav1.CreateOptions{})
	}
	return cs
}

func makeSecret(namespace, name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			key: []byte(value),
		},
	}
}

func TestWatch_InitialValue(t *testing.T) {
	cs := fakeClientset(makeSecret("prod", "mydb", "dsn", "postgres://host/db"))

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	val, ch, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if val != "postgres://host/db" {
		t.Errorf("initial value = %q, want %q", val, "postgres://host/db")
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}
}

func TestWatch_SecretNotFound(t *testing.T) {
	cs := fakeClientset() // no secrets

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	_, _, err := s.Watch(context.Background(), "missing", "namespace=prod&dsn=dsn")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestWatch_KeyNotFound(t *testing.T) {
	cs := fakeClientset(makeSecret("prod", "mydb", "password", "secret123"))

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	_, _, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestWatch_DefaultKey(t *testing.T) {
	cs := fakeClientset(makeSecret("default", "mydb", "dsn.txt", "postgres://host/db"))

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	val, _, err := s.Watch(context.Background(), "mydb", "namespace=default")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if val != "postgres://host/db" {
		t.Errorf("value = %q, want %q", val, "postgres://host/db")
	}
}

func TestWatch_Update(t *testing.T) {
	secret := makeSecret("prod", "mydb", "dsn", "postgres://old/db")
	cs := fake.NewSimpleClientset(secret)

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	val, ch, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if val != "postgres://old/db" {
		t.Fatalf("initial = %q, want %q", val, "postgres://old/db")
	}

	// Give the watch goroutine time to establish.
	time.Sleep(500 * time.Millisecond)

	// Update the secret via the API — the fake clientset propagates to watchers.
	updated := makeSecret("prod", "mydb", "dsn", "postgres://new/db")
	_, err = cs.CoreV1().Secrets("prod").Update(context.Background(), updated, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update secret: %v", err)
	}

	select {
	case newVal := <-ch:
		if newVal != "postgres://new/db" {
			t.Errorf("update = %q, want %q", newVal, "postgres://new/db")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for update")
	}
}

func TestCloseWatch(t *testing.T) {
	cs := fakeClientset(makeSecret("prod", "mydb", "dsn", "postgres://host/db"))

	s := NewStrategy()
	s.clientset = cs

	_, _, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	err = s.CloseWatch("mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("CloseWatch: %v", err)
	}

	s.mu.Lock()
	if len(s.watches) != 0 {
		t.Errorf("expected 0 watches after CloseWatch, got %d", len(s.watches))
	}
	s.mu.Unlock()

	s.Close()
}

func TestClose(t *testing.T) {
	cs := fakeClientset(
		makeSecret("prod", "db1", "dsn", "postgres://host/db1"),
		makeSecret("prod", "db2", "dsn", "postgres://host/db2"),
	)

	s := NewStrategy()
	s.clientset = cs

	s.Watch(context.Background(), "db1", "namespace=prod&dsn=dsn")
	s.Watch(context.Background(), "db2", "namespace=prod&dsn=dsn")

	s.Close()

	s.mu.Lock()
	if len(s.watches) != 0 {
		t.Errorf("expected 0 watches after Close, got %d", len(s.watches))
	}
	s.mu.Unlock()
}

func TestMultipleQueriesSamePath(t *testing.T) {
	cs := fakeClientset(makeSecret("prod", "mydb", "dsn", "postgres://host/db"))

	s := NewStrategy()
	s.clientset = cs
	defer s.Close()

	val1, ch1, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("Watch 1: %v", err)
	}
	val2, ch2, err := s.Watch(context.Background(), "mydb", "namespace=prod&dsn=dsn&extra=1")
	if err != nil {
		t.Fatalf("Watch 2: %v", err)
	}

	if val1 != val2 {
		t.Errorf("initial values differ: %q vs %q", val1, val2)
	}
	if ch1 == nil || ch2 == nil {
		t.Fatal("expected non-nil channels")
	}
}
