package main

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func strPtr(s string) *string { return &s }

func TestWriteIndexGroupsVersionsUnderSingleKey(t *testing.T) {
	assets := []*Asset{
		{Name: "totvs-ai-external-secret", SHA: "sha256:aaa", Tags: []*string{strPtr("0.1.0")}},
		{Name: "totvs-ai-external-secret", SHA: "sha256:bbb", Tags: []*string{strPtr("0.2.0")}},
		{Name: "totvs-ai-config", SHA: "sha256:ccc", Tags: []*string{strPtr("0.1.6")}},
		{Name: "untagged-chart", SHA: "sha256:ddd"},
	}

	var buf bytes.Buffer
	writeIndex(&buf, assets)
	out := buf.String()

	if got := strings.Count(out, "  totvs-ai-external-secret:\n"); got != 1 {
		t.Errorf("expected exactly 1 entries key for totvs-ai-external-secret, got %d\n%s", got, out)
	}

	var index struct {
		APIVersion string `json:"apiVersion"`
		Entries    map[string][]struct {
			Created     string   `json:"created"`
			Description string   `json:"description"`
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			Version     string   `json:"version"`
			Digest      string   `json:"digest"`
			URLs        []string `json:"urls"`
		} `json:"entries"`
	}
	if err := yaml.UnmarshalStrict([]byte(out), &index); err != nil {
		t.Fatalf("index is not valid strict YAML (duplicate keys?): %v\n%s", err, out)
	}

	versions := index.Entries["totvs-ai-external-secret"]
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions for totvs-ai-external-secret, got %d", len(versions))
	}
	if versions[0].Version != "0.1.0" || versions[1].Version != "0.2.0" {
		t.Errorf("unexpected versions: %+v", versions)
	}
	if versions[1].Digest != "bbb" {
		t.Errorf("expected digest bbb for 0.2.0, got %s", versions[1].Digest)
	}

	if len(index.Entries["totvs-ai-config"]) != 1 {
		t.Errorf("expected 1 version for totvs-ai-config")
	}
	if _, ok := index.Entries["untagged-chart"]; ok {
		t.Errorf("untagged chart must not appear in the index")
	}
}

func TestWriteIndexMultipleTagsOnOneDigest(t *testing.T) {
	assets := []*Asset{
		{Name: "chart-a", SHA: "sha256:eee", Tags: []*string{strPtr("1.0.0"), strPtr("1.0.1")}},
	}

	var buf bytes.Buffer
	writeIndex(&buf, assets)
	out := buf.String()

	if !strings.Contains(out, "version: 1.0.0") || !strings.Contains(out, "version: 1.0.1") {
		t.Errorf("expected one entry per tag, got:\n%s", out)
	}
}
