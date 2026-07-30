package repo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
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
		// Match by basename so nested entries like "0.19.0/meta.yaml" still
		// match. First match wins for each (root-level beats nested).
		if filepath.Base(hdr.Name) == "manifest.json" && manifestData == nil {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read manifest: %w", err)
			}
			manifestData = data
		} else if filepath.Base(hdr.Name) == "meta.yaml" && metaData == nil {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read meta: %w", err)
			}
			metaData = data
		}
	}
	return manifestData, metaData, nil
}
