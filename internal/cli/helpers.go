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
	"path/filepath"
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
	return runTarExtractTo(tarballPath, fileName, destDir+"/"+fileName)
}

// runTarExtractTo is like runTarExtract but lets the caller specify the
// final output path explicitly (so the script's archived location can
// differ from where execScript expects to find it).
func runTarExtractTo(tarballPath, fileName, outPath string) error {
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
			if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
				return fmt.Errorf("mkdir for %s: %w", outPath, err)
			}
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	}
	return fmt.Errorf("file %s not found in tarball", fileName)
}
