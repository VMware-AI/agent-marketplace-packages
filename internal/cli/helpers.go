package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// jsonUnmarshal wraps encoding/json so callers don't all import it.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// sha256New returns a fresh SHA-256 hasher.
func sha256New() sha256Type { return sha256.New() }

// sha256Type is the interface returned by sha256New, hiding the concrete
// hash.Hash so callers only see Sum(nil) → []byte.
type sha256Type interface {
	Write(p []byte) (n int, err error)
	Sum(b []byte) []byte
}

// hexEncode encodes bytes to lowercase hex.
func hexEncode(b []byte) string { return hex.EncodeToString(b) }

// runTarExtract pulls a single named regular file out of a tar.gz.
//
// Uses the stdlib archive/tar (no extra deps). Streams the archive; we
// only write the bytes for the requested file.
func runTarExtract(tarballPath, fileName, destDir string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Name == fileName {
			out, err := os.Create(destDir + "/" + fileName)
			if err != nil {
				return err
			}
			defer out.Close()
			if _, err := io.Copy(out, tr); err != nil {
				return err
			}
			out.Close()
			if err := os.Chmod(out.Name(), 0755); err != nil {
				return fmt.Errorf("chmod: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("file %s not found in tarball", fileName)
}