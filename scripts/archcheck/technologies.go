package main

// This file can contain additional rules/checks or helper functions for disallowed technology uses if needed.
// Currently, imports.go checks:
// 1. Gin outside api
// 2. os/exec outside infrastructure (or pkg)
// 3. SQLite outside infrastructure/database
// 4. database/sql in application
// 5. os.Getenv outside config/app
