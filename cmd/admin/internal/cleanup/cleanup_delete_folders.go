// cmd/admin/cleanup_delete_folders.go — hardcoded folder ID list deletion
//
// runDeleteSpecificFolders deletes a fixed list of Drive folder IDs
// by first trying DeletionSvc (Artlist and Stock DB deletion) and
// falling back to direct Drive admin deletion.
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func RunDeleteSpecificFolders(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Admin == nil {
		return fmt.Errorf("drive admin port is not available")
	}
	driveAdmin := root.Drive.Admin
	deletionSvc := root.Maint.DeletionSvc

	ctx := cli.CmdContext()
	folderIDs := append([]string(nil), args...)
	if len(folderIDs) == 0 {
		folderIDs = []string{
			"1M7qauleXrKliDsouP4H9Iodl_y2Z0o8-",
			"10hGPV1wqV6a-ZbToDSIHjM8CodJPm3Hg",
			"1OYP_pPMqbGffhxqtE4e0_zzUhviJo_Hu",
			"1FYqGXJfWkgr1MpMUKIa5qujTOr4Ld5x6",
			"1SXl-eitwXLvZBkFyeGIKIJWnZgmH6k5S",
			"1TEvlglo4KU-TrvJXs4_I9N5bDFyp2yO2",
			"17TNgc4l4Kx2zx237EDr5_Iu4bSBrNlNN",
			"1s6tETo-59Dd8LkXGwDiDAxwc0T_ACbfl",
			"1OCgsOYhRHFIGOsW9UGXDDkZcrnyuppkI",
			"1StoxaT_MVM_GIKWT4PrKhmaBj8IZjc_u",
			"1LxcaHzmO8F1fKAMftifytM3xIxiAwSHZ",
			"10FOpE-yHPpo_BQ7VCJB2Hx75xy9m01ea",
			"1Fzy-ofMDePJBpdv9kxs7klb05792ANxk",
			"1UzhMeE8iN4RsgH9GVctMQ_Tm5xHT75se",
			"1Cirzat1wv7qMlyLhb0OV-t9N3zon63Wo",
			"1tiR3aeB4W1cN84SSfb54yeVQrBMCpRWu",
			"1qUNhNJig0gHRKLux8Numlzti5mS5Yx_N",
			"1U3Jgrfa-nMkJDxcw1UjneqYfFRdtmW6W",
			"1geGeH-rxPRRacUtbYa_FA_iH8BGXjY5b",
			"1hqLR1B2bLe9Vc438xzPOMkatc44xqx42",
			"1eR_ehplczPGsuwypd_N1ZTAmN5_4AdvU",
			"1-NNBwwucOD5dsL2wsR4bNWo8HNFAnYND",
			"1DFyMZhweZpn636GpA8SJ_PHweGczb8x6",
			"1132AnWcbsdGjTmoZAWmB_RKdFloJNo2w",
			"1WTVgfxXiEBTPBqFDeRSlOcvY5E7tsoCw",
			"1EKVF3YHDtQU6sMdolBF2OuVnmNdMbhMs",
			"1Ih335jighEGqz27_OSbz_9I6Oe17pPD7",
			"1P7S2D2zNmhNsuSgNlVVNUjFnRq0yQ6Wc",
			"1T1QxQhjNcMkDSXDjDiGZH9mtflPwucQ8",
			"1RO23-aYSECwHUNGoeatMeKzteZ5yFGE-",
			"1mZifx2S2EA0EYiBVJZm5U5gOc-4kiHXz",
			"1tMmvVPLeIQlAXGBJ8q0GWdS92INLFKE6",
			"1acx0qvYlRUxc6FSC1B19RcU4-N3kAJLZ",
			"1HwKXo-szV4BjnkUZAw34I5vgzNfWM83W",
			"1t0SM9N2cp6B-bGhrYoDKQiEi09gxVqIG",
			"1mJxbaMrSr9XUAyKEyMsJhyxnkSaIO3RY",
			"1Qatr7H-NoKiolIFh6SIrhM19nlYWdOPj",
		}
	}

	for _, id := range folderIDs {
		fmt.Printf("Deleting %s... ", id)

		if len(args) > 0 {
			if driveAdmin == nil {
				fmt.Println("FAILED (drive admin unavailable)")
				continue
			}
			if err := driveAdmin.DeleteFolder(ctx, id); err != nil {
				fmt.Printf("FAILED: %v\n", err)
				continue
			}
			fmt.Println("OK (Drive)")
			if deletionSvc != nil {
				_ = deletionSvc.DeleteClip(ctx, "artlist", id, true)
				_ = deletionSvc.DeleteClip(ctx, "stock", id, true)
			}
			continue
		}

		if deletionSvc != nil {
			err := deletionSvc.DeleteClip(ctx, "artlist", id, true)
			if err == nil {
				fmt.Println("OK (Artlist DB)")
				continue
			}

			err = deletionSvc.DeleteClip(ctx, "stock", id, true)
			if err == nil {
				fmt.Println("OK (Stock DB)")
				continue
			}
		}

		if driveAdmin == nil {
			fmt.Println("FAILED (drive uploader unavailable)")
			continue
		}
		err := driveAdmin.DeleteFolder(ctx, id)
		if err == nil {
			fmt.Println("OK (Drive only)")
		} else {
			fmt.Printf("FAILED: %v\n", err)
		}
	}

	fmt.Println("Done")
	return nil
}
