package main

import (
"context"
"encoding/json"
"fmt"
"log"
"os"
"time"

"golang.org/x/oauth2"
"golang.org/x/oauth2/google"
)

func main() {
ctx := context.Background()

// Read credentials
credBytes, err := os.ReadFile("credentials.json")
if err != nil {
log.Fatalf("Failed to read credentials: %v", err)
}

// Parse OAuth config
oauthConfig, err := google.ConfigFromJSON(credBytes, 
"https://www.googleapis.com/auth/drive.file",
"https://www.googleapis.com/auth/drive.metadata",
)
if err != nil {
log.Fatalf("Failed to parse credentials: %v", err)
}

// Read existing token
tokenData, err := os.ReadFile("token.json")
if err != nil {
log.Fatalf("Failed to read token: %v", err)
}

// Parse token
var token oauth2.Token
if err := json.Unmarshal(tokenData, &token); err != nil {
log.Fatalf("Failed to parse token: %v", err)
}

fmt.Printf("Current token expiry: %v\n", token.Expiry)
fmt.Printf("Current time: %v\n", time.Now())

// Create token source to refresh
tokenSource := oauthConfig.TokenSource(ctx, &token)

// Force refresh
newToken, err := tokenSource.Token()
if err != nil {
log.Fatalf("Failed to refresh token: %v", err)
}

// Save new token
newTokenData, err := json.MarshalIndent(newToken, "", "  ")
if err != nil {
log.Fatalf("Failed to marshal new token: %v", err)
}

if err := os.WriteFile("token.json", newTokenData, 0600); err != nil {
log.Fatalf("Failed to write token: %v", err)
}

fmt.Printf("✅ Token refreshed and saved!\n")
fmt.Printf("📅 New expiry: %v\n", newToken.Expiry)
}
