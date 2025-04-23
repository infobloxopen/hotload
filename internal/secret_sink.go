package internal

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	randomLen = 8
)

// SecretSink returns a
type SecretSink interface {
	Add(actualSecret string) (randomSecret string, err error)
}

// RandomSecretSink binds a secret value with a random value.
// The count of secret/random pairs stored is limited. When full,
// any new secret/random pair added results in deleting the oldest pair.
// If a secret has already been added previously, a new secret/random
// pair is not created/added, but its last-used timestamp is updated.
type RandomSecretSink struct {
	sync.Mutex
	secretStore map[string]*RandomSecretData
	maxCount    int
}

type RandomSecretData struct {
	actualSecret string
	randomSecret string
	lastUsedTime time.Time
}

// NewRandomSecretSink returns a SecretSink that can hold
// `maxCount` secret/random pairs.
func NewRandomSecretSink(maxCount int) SecretSink {
	return &RandomSecretSink{
		secretStore: make(map[string]*RandomSecretData),
		maxCount:    maxCount,
	}
}

// Add is thread-safe and creates/binds a random value with the secret.
// If secret has already been previously added, then it returns
// the secret's previously-created random value.
// The secret/random pair's last-used timestamp is updated.
// If full, the oldest secret/random pair is deleted.
func (rss *RandomSecretSink) Add(actualSecret string) (randomSecret string, err error) {
	rss.Lock()
	defer rss.Unlock()

	secretData, found := rss.secretStore[actualSecret]
	if found && secretData != nil {
		secretData.lastUsedTime = time.Now()
		return secretData.randomSecret, nil
	}

	if len(rss.secretStore) >= rss.maxCount {
		oldestTime := time.Now()
		var oldestData *RandomSecretData
		for _, data := range rss.secretStore {
			if oldestTime.After(data.lastUsedTime) {
				oldestTime = data.lastUsedTime
				oldestData = data
			}
		}
		delete(rss.secretStore, oldestData.actualSecret)
	}

	guid, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	randomSecret = guid.String()[:randomLen]

	secretData = &RandomSecretData{
		actualSecret: actualSecret,
		randomSecret: randomSecret,
		lastUsedTime: time.Now(),
	}

	rss.secretStore[actualSecret] = secretData
	return secretData.randomSecret, nil
}
