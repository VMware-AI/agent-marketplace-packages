package repo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// ExtractManifestAndMeta extracts manifest.json and meta.yaml from a gzipped
// tarball. Returns (manifestBytes, metaBytes, error). If meta.yaml is not
// present (pre-refactor tarballs), metaBytes is nil and error is nil.
func ExtractManifestAndMeta(tarballData []byte) (manifestData []byte, metaData []byte, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarballData))
	if err != nil {
		return nil, nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("tar next: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Match top-level manifest.json or meta.yaml only — ignore nested copies.
		if hdr.Name == "manifest.json" || hdr.Name == "./manifest.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read manifest: %w", err)
			}
			manifestData = data
		} else if hdr.Name == "meta.yaml" || hdr.Name == "./meta.yaml" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read meta: %w", err)
			}
			metaData = data
		}
	}
	return manifestData, metaData, nil
}