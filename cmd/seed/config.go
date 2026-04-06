package main

import (
	_ "embed"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"
)

//go:embed tiers.toml
var tiersData []byte

// validTiers lists the accepted --size values.
var validTiers = []string{"small", "medium", "large", "enterprise"}

// ─── TOML structure ─────────────────────────────────────────────────────────

type tiersFile struct {
	Base  baseConfig           `toml:"base"`
	Tiers map[string]tierEntry `toml:"tiers"`
}

type baseConfig struct {
	S3  baseS3Config  `toml:"s3"`
	EC2 baseEC2Config `toml:"ec2"`
}

type baseS3Config struct {
	Buckets []bucketDef `toml:"buckets"`
}

type baseEC2Config struct {
	AMIs      []amiDef      `toml:"amis"`
	Instances []instanceDef `toml:"instances"`
}

type tierEntry struct {
	S3  tierS3Extras  `toml:"s3"`
	EC2 tierEC2Extras `toml:"ec2"`
}

// ─── Resource definitions ───────────────────────────────────────────────────

type bucketDef struct {
	Name string `toml:"name"`
}

type amiDef struct {
	Name         string `toml:"name"`
	Architecture string `toml:"architecture"`
}

type instanceDef struct {
	Name         string `toml:"name"`
	AMIRef       string `toml:"ami_ref"`
	InstanceType string `toml:"instance_type"`
	Stopped      bool   `toml:"stopped"`
}

// ─── Tier extras ────────────────────────────────────────────────────────────

type tierS3Extras struct {
	ExtraBuckets         int `toml:"extra_buckets"`
	ObjectsPerExtraBucket int `toml:"objects_per_extra_bucket"`
}

type tierEC2Extras struct {
	ExtraAMIs      int     `toml:"extra_amis"`
	ExtraInstances int     `toml:"extra_instances"`
	StopFraction   float64 `toml:"stop_fraction"`
}

// ─── Merged config ──────────────────────────────────────────────────────────

// SeedConfig is the fully resolved configuration for a seed run.
type SeedConfig struct {
	S3  S3SeedConfig
	EC2 EC2SeedConfig
}

type S3SeedConfig struct {
	Buckets               []bucketDef
	ExtraBuckets          int
	ObjectsPerExtraBucket int
}

type EC2SeedConfig struct {
	AMIs           []amiDef
	Instances      []instanceDef
	ExtraAMIs      int
	ExtraInstances int
	StopFraction   float64
}

// loadConfig parses the embedded TOML and returns the merged config for the requested tier.
func loadConfig(tier string) (*SeedConfig, error) {
	var f tiersFile
	if err := toml.Unmarshal(tiersData, &f); err != nil {
		return nil, fmt.Errorf("parsing tiers.toml: %w", err)
	}

	entry, ok := f.Tiers[tier]
	if !ok {
		return nil, fmt.Errorf("unknown tier %q (valid: %v)", tier, validTiers)
	}

	return &SeedConfig{
		S3: S3SeedConfig{
			Buckets:               f.Base.S3.Buckets,
			ExtraBuckets:          entry.S3.ExtraBuckets,
			ObjectsPerExtraBucket: entry.S3.ObjectsPerExtraBucket,
		},
		EC2: EC2SeedConfig{
			AMIs:           f.Base.EC2.AMIs,
			Instances:      f.Base.EC2.Instances,
			ExtraAMIs:      entry.EC2.ExtraAMIs,
			ExtraInstances: entry.EC2.ExtraInstances,
			StopFraction:   entry.EC2.StopFraction,
		},
	}, nil
}
