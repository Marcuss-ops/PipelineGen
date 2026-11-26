package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, _ := sql.Open("pgx", os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN"))
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, id := range []string{"yt_k9FS4UZPVVI_259_309_v1", "yt_rLccejx00HA_0_59_v1", "yt_J7GpwFrGsiU_105_157_v1", "yt_JP6BrYSa_yw_0_58_v1"} {
		var lp, lk, uri string
		_ = db.QueryRowContext(ctx, `select COALESCE(metadata_json->>'local_path','') from media_assets where id=$1`, id).Scan(&lp)
		_ = db.QueryRowContext(ctx, `select COALESCE(location_kind,''), COALESCE(uri,'') from asset_locations where asset_id=$1 order by location_kind limit 1`, id).Scan(&lk, &uri)
		fmt.Printf("%s | local_path=%q | first_loc=%s:%s\n", id, lp, lk, uri)
	}
}
