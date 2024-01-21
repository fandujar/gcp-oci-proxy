package main

import (
	"context"
	"encoding/json"
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

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	artifactregistrypb "cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
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

func isRequestAuthorized(input string) bool {
	auths, err := getAuths()
	if err != nil {
		log.Println(err)
		return false
	}

	for _, auth := range auths {
		if input == auth {
			return true
		}
	}
	return false
}

func getAuths() ([]string, error) {
	auths := os.Getenv("AUTHS")
	if auths == "" {
		return nil, fmt.Errorf("missing auths")
	}
	return strings.Split(auths, ","), nil
}

func getAssetsHandler(ctx context.Context) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var path = chi.URLParam(r, "asset")
		if strings.Contains(path, ".ico") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("404 Not Found\n"))
			return
		}

		// user, pwd, ok := r.BasicAuth()
		// if !ok {
		// 	log.Println("missing basic auth")
		// 	w.WriteHeader(http.StatusUnauthorized)
		// 	w.Write([]byte("401 Unauthorized\n"))
		// 	return
		// }

		// if !(ok && isRequestAuthorized(user+":"+pwd)) {
		// 	log.Println("unauthorized:", user)
		// 	w.WriteHeader(http.StatusUnauthorized)
		// 	w.Write([]byte("401 Unauthorized\n"))
		// }

		reader, err := getImageMetadataReader(ctx, path)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}

		if _, err := io.Copy(w, reader); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}
	}
}

func getImageMetadataReader(ctx context.Context, path string) (io.Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("missing path")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	c, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	formattedPath, err := formatPath(path)
	if err != nil {
		return nil, err
	}
	log.Println("formattedPath:", formattedPath)
	req := &artifactregistrypb.GetDockerImageRequest{
		Name: formattedPath,
	}

	resp, err := c.GetDockerImage(ctx, req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	reader := strings.NewReader(string(body))

	return reader, nil
}

func formatPath(path string) (string, error) {
	project := os.Getenv("PROJECT")
	if project == "" {
		return "", fmt.Errorf("missing project")
	}

	repository := os.Getenv("REPOSITORY")
	if repository == "" {
		return "", fmt.Errorf("missing repository")
	}

	region := os.Getenv("REGION")
	if region == "" {
		region = "us-central1"
	}

	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	return fmt.Sprintf("projects/%s/locations/%s/repositories/%s/dockerImages/%s", project, region, repository, path), nil
}
func main() {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	ctx := context.Background()
	router := defaultRouter(nil)
	router.Get("/{asset}", getAssetsHandler(ctx))

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
