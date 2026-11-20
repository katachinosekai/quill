package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func keyPath() string {
	if p := os.Getenv("QUILL_RELEASE_KEY"); p != "" {
		return p
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "quill-release", "release.key")
}

func main() {
	gen := flag.Bool("genkey", false, "create the release signing key (once) and print its public key")
	dir := flag.String("sign", "", "write SHA256SUMS and SHA256SUMS.sig for the release files in this folder")
	flag.Parse()
	var err error
	switch {
	case *gen:
		err = genKey()
	case *dir != "":
		err = sign(*dir)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "quill-sign:", err)
		os.Exit(1)
	}
}

func genKey() error {
	p := keyPath()
	if _, err := os.Stat(p); err == nil {
		return errors.New("a key already exists at " + p + "; refusing to overwrite it")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func loadKey() (ed25519.PrivateKey, error) {
	p := keyPath()
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, errors.New(p + " must be readable only by you (chmod 600)")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != ed25519.PrivateKeySize {
		return nil, errors.New("bad key file")
	}
	return ed25519.PrivateKey(k), nil
}

func sign(dir string) error {
	key, err := loadKey()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "SHA256SUMS" || n == "SHA256SUMS.sig" {
			continue
		}
		if strings.HasSuffix(n, ".rpm") || strings.HasSuffix(n, ".AppImage") || strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".apk") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return errors.New("no release files in " + dir)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), n)
	}
	sums := []byte(b.String())
	sig := ed25519.Sign(key, sums)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}
