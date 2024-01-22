package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"

	"helm.sh/helm/v3/pkg/registry"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	artifactregistrypb "cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"google.golang.org/api/iterator"
)

type Config struct {
	Project    string
	Repository string
	Region     string
	Port       string
	Credential string
}

type Repository struct {
	Assets []*Asset `json:"assets"`
}

type Asset struct {
	Name      string    `json:"name"`
	SHA       string    `json:"sha"`
	RawName   string    `json:"raw_name"`
	URI       string    `json:"uri"`
	MediaType string    `json:"media_type"`
	Tags      []*string `json:"tags"`
}

var (
	RepositoryDB *Repository = &Repository{}
)

func newServer(router *chi.Mux) *http.Server {
	listen := os.Getenv("PORT")
	if listen == "" {
		listen = ":8080"
	}

	return &http.Server{
		Addr:        listen,
		Handler:     router,
		ReadTimeout: 5 * time.Second,
	}
}

func defaultRouter(healthCheck func(w http.ResponseWriter, r *http.Request)) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Logger, middleware.Recoverer)
	if healthCheck == nil {
		healthCheck = defaultHealthCheck
	}
	router.Get("/health", healthCheck)
	return router
}

func defaultHealthCheck(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}

func formatPath(config *Config) (string, error) {
	return fmt.Sprintf(
		"projects/%s/locations/%s/repositories/%s",
		config.Project, config.Region, config.Repository,
	), nil
}

func newConfig() (*Config, error) {
	project := os.Getenv("PROJECT")
	if project == "" {
		return nil, fmt.Errorf("missing project")
	}

	repository := os.Getenv("REPOSITORY")
	if repository == "" {
		return nil, fmt.Errorf("missing repository")
	}

	region := os.Getenv("REGION")
	if region == "" {
		region = "us-central1"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	}

	credential := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credential == "" {
		return nil, fmt.Errorf("missing credential")
	}

	return &Config{
		Project:    project,
		Repository: repository,
		Region:     region,
		Port:       port,
		Credential: credential,
	}, nil
}

func initDB(ctx context.Context, config *Config, client *artifactregistry.Client) error {
	formattedPath, err := formatPath(config)
	if err != nil {
		return err
	}

	req := &artifactregistrypb.ListDockerImagesRequest{
		Parent: formattedPath,
	}

	it := client.ListDockerImages(ctx, req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil && err != iterator.Done {
			return err
		}

		name, sha, err := extractNameAndSha(resp.Name)
		if err != nil {
			return err
		}

		var asset *Asset = &Asset{
			Name:      name,
			SHA:       sha,
			RawName:   resp.Name,
			URI:       resp.Uri,
			MediaType: resp.MediaType,
		}

		for _, tag := range resp.Tags {
			asset.Tags = append(asset.Tags, &tag)
		}

		RepositoryDB.Assets = append(RepositoryDB.Assets, asset)
	}
	return nil
}

func extractNameAndSha(input string) (name, sha string, err error) {
	parts := strings.Split(input, "/")

	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid input format")
	}

	namePart := parts[len(parts)-1]
	nameParts := strings.Split(namePart, "@")

	if len(nameParts) != 2 {
		return "", "", fmt.Errorf("invalid name and SHA format")
	}

	name = nameParts[0]
	sha = nameParts[1]

	return name, sha, nil
}

func getCredential(config *Config) (string, string, error) {
	credentialFile, err := os.Open(config.Credential)
	if err != nil {
		return "", "", err
	}
	defer credentialFile.Close()

	credentialBytes, err := io.ReadAll(credentialFile)
	if err != nil {
		return "", "", err
	}

	return "_json_key", string(credentialBytes), nil
}

func main() {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	config, err := newConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	c, err := artifactregistry.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	if err := initDB(ctx, config, c); err != nil {
		log.Fatalf("failed to init db. error: %v", err)
	}

	client, err := registry.NewClient(registry.ClientOptDebug(true))
	if err != nil {
		log.Fatal(err)
	}

	router := defaultRouter(nil)
	router.Get("/{assetName}@{assetSHA}", func(w http.ResponseWriter, r *http.Request) {
		var assetName = chi.URLParam(r, "assetName")
		var assetSHA = chi.URLParam(r, "assetSHA")
		log.Println(assetName, assetSHA)
		for _, asset := range RepositoryDB.Assets {
			if asset.Name == assetName && asset.SHA == assetSHA {
				user, credential, err := getCredential(config)
				if err != nil {
					log.Fatal(err)
				}
				err = client.Login(asset.URI, registry.LoginOptBasicAuth(
					user,
					credential,
				))
				if err != nil {
					log.Fatal(err)
				}
				result, err := client.Pull(asset.URI)
				if err != nil {
					log.Fatal(err)
				}
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.tgz", result.Chart.Meta.Name, result.Chart.Meta.Version))
				w.WriteHeader(http.StatusOK)
				reader := bytes.NewReader(result.Chart.Data)
				io.Copy(w, reader)
				return
			}
		}
	})

	router.Get("/{assetName}:{assetTag}", func(w http.ResponseWriter, r *http.Request) {
		var assetName = chi.URLParam(r, "assetName")
		var assetTag = chi.URLParam(r, "assetTag")

		for _, asset := range RepositoryDB.Assets {
			if asset.Name == assetName {
				for _, tag := range asset.Tags {
					if *tag == assetTag {
						user, credential, err := getCredential(config)
						if err != nil {
							log.Fatal(err)
						}
						err = client.Login(asset.URI, registry.LoginOptBasicAuth(
							user,
							credential,
						))
						if err != nil {
							log.Fatal(err)
						}
						result, err := client.Pull(asset.URI)
						if err != nil {
							log.Fatal(err)
						}
						w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.tgz", result.Chart.Meta.Name, result.Chart.Meta.Version))
						reader := bytes.NewReader(result.Chart.Data)
						io.Copy(w, reader)
						return
					}
				}
			}
		}
	})

	server := newServer(router)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	<-done
	log.Println("stopping")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("couldn't stop server: %+v", err)
	}
}
