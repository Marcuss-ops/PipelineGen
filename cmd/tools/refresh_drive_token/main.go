package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	driveapi "google.golang.org/api/drive/v3"

	driveclient "velox/go-master/internal/upload/drive"
	appconfig "velox/go-master/pkg/config"
)

func main() {
	cfg := appconfig.Get()
	var (
		credentialsPath = flag.String("credentials", cfg.GetCredentialsPath(), "Path to OAuth client credentials JSON")
		tokenPath       = flag.String("token", cfg.GetTokenPath(), "Path to the token JSON to write")
		port            = flag.Int("port", 8085, "Local callback port for OAuth redirect")
		openBrowser     = flag.Bool("open-browser", true, "Open the browser automatically")
	)
	flag.Parse()

	credBytes, err := os.ReadFile(*credentialsPath)
	if err != nil {
		log.Fatalf("read credentials: %v", err)
	}

	scopes := []string{
		driveapi.DriveFileScope,
		driveapi.DriveMetadataScope,
	}
	oauthConfig, err := google.ConfigFromJSON(credBytes, scopes...)
	if err != nil {
		log.Fatalf("parse credentials: %v", err)
	}

	redirectURL := fmt.Sprintf("http://localhost:%d/callback", *port)
	oauthConfig.RedirectURL = redirectURL

	state := fmt.Sprintf("velox-drive-%d", time.Now().UnixNano())
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("missing code")
			return
		}
		_, _ = w.Write([]byte("Token received. You can close this tab."))
		codeCh <- code
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	authURL := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Println("Open this URL and authorize Drive access:")
	fmt.Println(authURL)
	fmt.Println("Redirect URI:", redirectURL)
	if *openBrowser {
		_ = openURL(authURL)
	}

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		log.Fatalf("oauth callback: %v", err)
	case <-time.After(10 * time.Minute):
		log.Fatal("timeout waiting for OAuth callback")
	}

	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		log.Fatalf("exchange code: %v", err)
	}

	if err := driveclient.SaveToken(*tokenPath, token); err != nil {
		log.Fatalf("save token: %v", err)
	}

	fmt.Printf("Saved token to %s\n", *tokenPath)
	fmt.Printf("Expiry: %s\n", token.Expiry.Format(time.RFC3339))

	if err := verifyToken(*credentialsPath, *tokenPath); err != nil {
		log.Fatalf("verification failed: %v", err)
	}

	fmt.Println("Drive token verified successfully.")
}

func verifyToken(credentialsPath, tokenPath string) error {
	client, err := driveclient.NewClient(context.Background(), driveclient.Config{
		CredentialsFile: credentialsPath,
		TokenFile:       tokenPath,
		Scopes: []string{
			driveapi.DriveFileScope,
			driveapi.DriveMetadataScope,
		},
	})
	if err != nil {
		return err
	}
	_, err = client.ListFoldersNoRecursion(context.Background(), driveclient.ListFoldersOptions{MaxItems: 1})
	return err
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", url).Run()
		}
	}
	return fmt.Errorf("no browser opener found")
}
