//go:build ignore
// +build ignore

// raw.go — diagnose response shape mismatch by dumping raw body bytes
// (truncated). Run via: go run raw.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
)

func main() {
	loadDotenv()

	httpClient, err := ncloud.NewHTTPClient(ncloud.Config{
		Env:     ncloud.EnvPublic,
		Creds:   auth.DefaultChain(),
		Timeout: 15 * time.Second,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewHTTPClient:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://wms.apigw.ntruss.com/api/v1/scenarios", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewRequest:", err)
		os.Exit(1)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Do:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP %d (%d bytes)\n", resp.StatusCode, len(body))
	preview := string(body)
	if len(preview) > 800 {
		preview = preview[:800] + "..."
	}
	fmt.Println("body[0..800]:")
	fmt.Println(preview)
}

func loadDotenv() {
	cwd, _ := os.Getwd()
	for _, p := range []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, "..", "..", ".env"),
	} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			eq := strings.Index(line, "=")
			if eq < 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
				val = val[1 : len(val)-1]
			}
			if key != "" && os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
		f.Close()
		return
	}
}
