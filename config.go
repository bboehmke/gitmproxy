package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/AdguardTeam/golibs/log"
	"github.com/dustin/go-humanize"
)

// ByteSize is a custom type that represents a size in bytes, with a method to decode from a string.
type ByteSize int64

func (b *ByteSize) UnmarshalText(data []byte) error {
	value := string(data)
	// Support values like 10MB, 5GB, 100K, etc.
	value = strings.TrimSpace(strings.ToUpper(value))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(value, "GB"):
		multiplier = 1 << 30
		value = strings.TrimSuffix(value, "GB")
	case strings.HasSuffix(value, "MB"):
		multiplier = 1 << 20
		value = strings.TrimSuffix(value, "MB")
	case strings.HasSuffix(value, "KB"):
		multiplier = 1 << 10
		value = strings.TrimSuffix(value, "KB")
	case strings.HasSuffix(value, "B"):
		multiplier = 1
		value = strings.TrimSuffix(value, "B")
	}
	num, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	*b = ByteSize(num * float64(multiplier))
	return nil
}

// Config holds the configuration for the cache system.
type Config struct {
	ListenAddr    string        `env:"LISTEN_ADDR" envDefault:":8090"`
	CacheDir      string        `env:"CACHE_DIR" envDefault:"cache"`      // directory where cache files are stored
	MaxSize       ByteSize      `env:"MAX_SIZE" envDefault:"10GB"`        // maximum size (in bytes) used for cache storage, 0 means unlimited
	EntryMaxSize  ByteSize      `env:"ENTRY_MAX_SIZE" envDefault:"500MB"` // maximum size (in bytes) for a single cached response, 0 means unlimited
	EntryTTL      time.Duration `env:"ENTRY_TTL" envDefault:"1h"`         // time-to-live for each cache entry, 0 means no expiration
	EnableLogging bool          `env:"ENABLE_LOGGING" envDefault:"true"`  // whether to enable logging of cache operations
}

func (c *Config) Print() {
	log.Info("Config:")
	log.Info("  ListenAddr: %s", c.ListenAddr)
	log.Info("  CacheDir: %s", c.CacheDir)
	log.Info("  MaxSize: %s", humanize.IBytes(uint64(c.MaxSize)))
	log.Info("  EntryMaxSize: %s", humanize.IBytes(uint64(c.EntryMaxSize)))
	log.Info("  EntryTTL: %s", c.EntryTTL)
	log.Info("  EnableLogging: %t", c.EnableLogging)
}
