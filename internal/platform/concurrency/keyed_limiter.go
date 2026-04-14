package concurrency

import (
	"errors"
	"sync"
)

var ErrEmptyKey = errors.New("key cannot be empty")

type KeyedLimiter struct {
	limiters sync.Map
	size     int
}

func NewKeyedLimiter(size int) *KeyedLimiter {
	if size <= 0 {
		size = 1
	}

	return &KeyedLimiter{size: size}
}

func (l *KeyedLimiter) Acquire(key string) (func(), error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	ch := l.get(key)
	ch <- struct{}{}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-ch
		})
	}, nil
}

func (l *KeyedLimiter) get(key string) chan struct{} {
	value, loaded := l.limiters.LoadOrStore(key, make(chan struct{}, l.size))
	if loaded {
		return value.(chan struct{})
	}

	return value.(chan struct{})
}
