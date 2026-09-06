package runner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const (
	stampMagic  = "J2NTRAIL"
	stampLength = 24
)

// StampConfig contains the application-specific values read by the generic runner.
type StampConfig struct {
	AppName     string   `json:"appName"`
	JarName     string   `json:"jarName"`
	JVMArgs     []string `json:"jvmArgs"`
	PayloadHash string   `json:"payloadHash"`
}

// Stamp appends payload and configuration to a precompiled runner binary.
func Stamp(runnerBinary, payload []byte, cfg StampConfig) ([]byte, error) {
	config, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal stamp config: %w", err)
	}
	if uint64(len(payload)) > ^uint64(0)-uint64(len(config))-stampLength {
		return nil, fmt.Errorf("stamp data is too large")
	}

	trailer := make([]byte, stampLength)
	copy(trailer, stampMagic)
	binary.LittleEndian.PutUint64(trailer[8:16], uint64(len(payload)))
	binary.LittleEndian.PutUint64(trailer[16:24], uint64(len(config)))

	result := make([]byte, 0, len(runnerBinary)+len(payload)+len(config)+stampLength)
	result = append(result, runnerBinary...)
	result = append(result, payload...)
	result = append(result, config...)
	result = append(result, trailer...)
	return result, nil
}

// ParseTrailer extracts the payload and configuration appended by Stamp.
func ParseTrailer(data []byte) ([]byte, StampConfig, error) {
	var cfg StampConfig
	if len(data) < stampLength {
		return nil, cfg, fmt.Errorf("runner binary has no stamp trailer")
	}
	trailer := data[len(data)-stampLength:]
	if string(trailer[:8]) != stampMagic {
		return nil, cfg, fmt.Errorf("invalid runner stamp magic")
	}
	payloadLength := binary.LittleEndian.Uint64(trailer[8:16])
	configLength := binary.LittleEndian.Uint64(trailer[16:24])
	dataLength := uint64(len(data) - stampLength)
	if payloadLength > dataLength || configLength > dataLength-payloadLength {
		return nil, cfg, fmt.Errorf("invalid runner stamp lengths")
	}
	configStart := dataLength - configLength
	payloadStart := configStart - payloadLength
	if configStart > uint64(len(data)) || payloadStart > configStart {
		return nil, cfg, fmt.Errorf("invalid runner stamp offsets")
	}
	configStartInt, payloadStartInt := int(configStart), int(payloadStart)
	if err := json.Unmarshal(data[configStartInt:len(data)-stampLength], &cfg); err != nil {
		return nil, cfg, fmt.Errorf("parse stamp config: %w", err)
	}
	return data[payloadStartInt:configStartInt], cfg, nil
}
