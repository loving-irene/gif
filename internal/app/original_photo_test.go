package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func TestOriginalWebPIsAcceptedWithoutReencoding(t *testing.T) {
	const fixture = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"
	want, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		t.Fatal(err)
	}
	got, err := imageData("data:image/webp;base64,"+fixture, 5*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("WebP bytes changed")
	}
	if _, err = imageData("data:image/jpeg;base64,"+fixture, 5*1024*1024); err == nil {
		t.Fatal("mislabeled WebP accepted")
	}
}

func TestOriginalHighResolutionPNGIsNotResized(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 6000, 4000))); err != nil {
		t.Fatal(err)
	}
	want := b.Bytes()
	if len(want) > 5*1024*1024 {
		t.Fatal("fixture too large")
	}
	got, err := imageData("data:image/png;base64,"+base64.StdEncoding.EncodeToString(want), 5*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("high resolution photo bytes changed")
	}
}
