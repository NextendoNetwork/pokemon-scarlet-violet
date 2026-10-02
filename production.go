package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func productionMode() bool { return os.Getenv("VIOLET_PRODUCTION") == "1" }

func requiredProductionFile(key string) error {
	path := os.Getenv(key)
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s must name an absolute file path", key)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must name an existing regular file", key)
	}
	return nil
}

func validateProductionConfig() error {
	if !productionMode() {
		return nil
	}
	for _, key := range []string{
		"NEXTENDO_SECRET_FILE", "NEXTENDO_INTERNAL_KEY_FILE", "NPLN_JWT_KEY",
		"CERT_FILE", "KEY_FILE", "NPLN_CA_CERT_FILE", "NPLN_TRADE_VALIDATION_KEY",
		"NPLN_TURN_PASSWORD_FILE",
	} {
		if err := requiredProductionFile(key); err != nil {
			return err
		}
	}
	if os.Getenv("NEXTENDO_SECRET") != "" || os.Getenv("NEXTENDO_INTERNAL_KEY") != "" {
		return fmt.Errorf("production secrets must be supplied by files")
	}
	if os.Getenv("NPLN_TURN_PASSWORD") != "" {
		return fmt.Errorf("NPLN_TURN_PASSWORD_FILE is required in production")
	}
	for _, key := range []string{"NPLN_ALLOW_UNVERIFIED", "VIOLET_LOCAL_TWO_PLAYERS", "NPLN_ALLOW_LEGACY_SIGNER", "NPLN_STANDALONE_GRPC"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("%s is incompatible with Violet production mode", key)
		}
	}
	if os.Getenv("NPLN_CERT_PROFILE") != "external" {
		return fmt.Errorf("NPLN_CERT_PROFILE must be external in production")
	}
	if os.Getenv("NPLN_NNCS_ENABLED") != "0" || os.Getenv("NPLN_UDP_OBSERVE_ADDRS") != "disabled" {
		return fmt.Errorf("NNCS and passive UDP observers must be disabled in production")
	}
	if !filepath.IsAbs(os.Getenv("VIOLET_RANKED_STATE_FILE")) {
		return fmt.Errorf("VIOLET_RANKED_STATE_FILE must be absolute")
	}
	if len(loadNextendoSecret()) < 32 {
		return fmt.Errorf("NEXTENDO_SECRET_FILE must contain a valid account signing secret")
	}
	if _, err := readInternalKey(); err != nil {
		return fmt.Errorf("NEXTENDO_INTERNAL_KEY_FILE is unusable: %w", err)
	}
	if key, _ := nplnSigningKey(); key == nil {
		return fmt.Errorf("NPLN_JWT_KEY is unusable")
	}
	return nil
}
