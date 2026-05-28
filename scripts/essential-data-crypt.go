package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/crypto"
)

func main() {
	sourceKey := flag.String("source-key", "", "encryption key used by the source database")
	targetKey := flag.String("target-key", "", "encryption key used by the target database")
	field := flag.String("field", "api_key", "JSON object field to re-encrypt")
	flag.Parse()

	if *targetKey == "" {
		fatalf("target-key is required")
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatalf("read stdin: %v", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(input, &rows); err != nil {
		fatalf("parse JSON payload: %v", err)
	}

	for i := range rows {
		raw, ok := rows[i][*field]
		if !ok || raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok || value == "" {
			continue
		}
		if *sourceKey == *targetKey {
			continue
		}
		if crypto.IsEncrypted(value) && *sourceKey == "" {
			fatalf("row %d field %q is encrypted but source-key is empty", i, *field)
		}
		plain, err := crypto.Decrypt(value, *sourceKey)
		if err != nil {
			fatalf("decrypt row %d field %q: %v", i, *field, err)
		}
		encrypted, err := crypto.Encrypt(plain, *targetKey)
		if err != nil {
			fatalf("encrypt row %d field %q: %v", i, *field, err)
		}
		rows[i][*field] = encrypted
	}

	output, err := json.Marshal(rows)
	if err != nil {
		fatalf("encode JSON payload: %v", err)
	}
	_, _ = os.Stdout.Write(output)
}

func fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	msg = strings.TrimSpace(msg)
	fmt.Fprintln(os.Stderr, "essential-data-crypt:", msg)
	os.Exit(1)
}
