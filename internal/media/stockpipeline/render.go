package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// renderChunk concatenates clips into a single output video with transition and effects.
func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string, noTransitions, noEffects, noAudio bool, chunkIdx int) error {
	if len(clips) == 0 {
		return fmt.Errorf("no clips to render")
	}
	videoCfg := s.cfg.Video.WithDefaults()

	// Se noTransitions e noEffects sono entrambi veri, usiamo la concatenazione rapida senza ri-codifica.
	if noTransitions && noEffects {
		concatPath := outputPath + ".concat.mp4"
		_ = os.Remove(concatPath)
		defer os.Remove(concatPath)

		if len(clips) == 1 {
			return s.ffmpegProc.Normalize(ctx, clips[0], outputPath, ffmpeg.NormalizeOptions{
				Width:            videoCfg.Width,
				Height:           videoCfg.Height,
				FPS:              videoCfg.FPS,
				Codec:            videoCfg.Codec,
				Preset:           videoCfg.Preset,
				CRF:              videoCfg.CRF,
				KeyframeInterval: videoCfg.KeyframeInterval,
				KeepAudio:        !noAudio,
			})
		}

		if err := s.ffmpegProc.MergeInputs(ctx, clips, concatPath); err != nil {
			return fmt.Errorf("concat chunk fast: %w", err)
		}

		return s.ffmpegProc.Normalize(ctx, concatPath, outputPath, ffmpeg.NormalizeOptions{
			Width:            videoCfg.Width,
			Height:           videoCfg.Height,
			FPS:              videoCfg.FPS,
			Codec:            videoCfg.Codec,
			Preset:           videoCfg.Preset,
			CRF:              videoCfg.CRF,
			KeyframeInterval: videoCfg.KeyframeInterval,
			KeepAudio:        !noAudio,
		})
	}

	// Altrimenti, costruiamo un filtro FFmpeg complesso per applicare effetti/transizioni.
	// NOTA: xfade richiede l'estrazione e la sovrapposizione temporale. Per semplicità e stabilità,
	// usiamo filtri di fade in/out o xfade se applicabile.
	// Carichiamo eventuali overlay se abilitati.
	var effects []string
	var err error
	if !noEffects {
		effects, err = loadEffects(s.pcfg.EffectsDir)
		if err != nil {
			s.log.Warn("failed to load effects, proceeding without effects", zap.Error(err))
		}
	}

	// Costruiamo gli argomenti di FFmpeg per generare il video con filtri complessi
	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}
	for _, clip := range clips {
		args = append(args, "-i", clip)
	}

	// Selezioniamo un overlay random se necessario
	var overlayFile string
	if len(effects) > 0 {
		overlayFile = effects[rng.Intn(len(effects))]
		args = append(args, "-i", overlayFile)
	}

	// filter_complex: concat con xfade o fade semplici
	var filterComplex strings.Builder
	inputCount := len(clips)

	// Costruiamo la catena di filtri complessi per il concatenamento
	// Per ciascun input applichiamo le transizioni e gli effetti
	for idx := 0; idx < inputCount; idx++ {
		// Costruiamo i filtri di base per questa singola clip
		var clipFilters []string
		clipFilters = append(clipFilters, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
			videoCfg.Width, videoCfg.Height, videoCfg.Width, videoCfg.Height, videoCfg.FPS))

		// 1. Applichiamo la transizione ogni 4 clip (fade out alla fine, fade in all'inizio della successiva)
		if !noTransitions {
			// Fade out alla fine della clip ogni 4 clip (indici 3, 7, 11...)
			if (idx+1)%4 == 0 {
				tIdx := (idx + 1) / 4
				transitionTypes := []string{
					"fadeblack", "fadewhite", "flash", "blur", "gray",
					"colorred", "colorblue", "colorgreen", "coloryellow",
					"colorpurple", "colororange", "colorpink", "negate",
					"vignette", "fastblur",
				}
				tType := transitionTypes[tIdx%len(transitionTypes)]

				s.log.Info("stock pipeline transition applied",
					zap.Int("chunk_index", chunkIdx),
					zap.Int("after_clip_index", idx),
					zap.String("type", tType),
				)

				fadeStart := float64(videoCfg.ClipDuration) - 0.5
				switch tType {
				case "fadeblack":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5", fadeStart))
				case "fadewhite":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=white", fadeStart))
				case "flash":
					flashStart := float64(videoCfg.ClipDuration) - 0.2
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.2:color=white", flashStart))
				case "blur":
					blurStart := float64(videoCfg.ClipDuration) - 0.5
					clipFilters = append(clipFilters, fmt.Sprintf("boxblur=15:enable='gt(t,%f)'", blurStart))
				case "gray":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=gray", fadeStart))
				case "colorred":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=red", fadeStart))
				case "colorblue":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=blue", fadeStart))
				case "colorgreen":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=green", fadeStart))
				case "coloryellow":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=yellow", fadeStart))
				case "colorpurple":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=purple", fadeStart))
				case "colororange":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=orange", fadeStart))
				case "colorpink":
					clipFilters = append(clipFilters, fmt.Sprintf("fade=t=out:st=%f:d=0.5:color=pink", fadeStart))
				case "negate":
					clipFilters = append(clipFilters, fmt.Sprintf("negate=enable='gt(t,%f)'", fadeStart))
				case "vignette":
					clipFilters = append(clipFilters, fmt.Sprintf("vignette=enable='gt(t,%f)'", fadeStart))
				case "fastblur":
					clipFilters = append(clipFilters, fmt.Sprintf("boxblur=30:enable='gt(t,%f)'", fadeStart))
				}
			}
			// Fade in all'inizio della clip successiva (indici 4, 8, 12...)
			if idx > 0 && idx%4 == 0 {
				tIdx := idx / 4
				transitionTypes := []string{
					"fadeblack", "fadewhite", "flash", "blur", "gray",
					"colorred", "colorblue", "colorgreen", "coloryellow",
					"colorpurple", "colororange", "colorpink", "negate",
					"vignette", "fastblur",
				}
				tType := transitionTypes[tIdx%len(transitionTypes)]

				s.log.Info("stock pipeline transition applied",
					zap.Int("chunk_index", chunkIdx),
					zap.Int("before_clip_index", idx),
					zap.String("type", tType),
				)

				switch tType {
				case "fadeblack":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5")
				case "fadewhite":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=white")
				case "flash":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.2:color=white")
				case "blur":
					clipFilters = append(clipFilters, "boxblur=15:enable='lt(t,0.5)'")
				case "gray":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=gray")
				case "colorred":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=red")
				case "colorblue":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=blue")
				case "colorgreen":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=green")
				case "coloryellow":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=yellow")
				case "colorpurple":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=purple")
				case "colororange":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=orange")
				case "colorpink":
					clipFilters = append(clipFilters, "fade=t=in:st=0:d=0.5:color=pink")
				case "negate":
					clipFilters = append(clipFilters, "negate=enable='lt(t,0.5)'")
				case "vignette":
					clipFilters = append(clipFilters, "vignette=enable='lt(t,0.5)'")
				case "fastblur":
					clipFilters = append(clipFilters, "boxblur=30:enable='lt(t,0.5)'")
				}
			}
		}

		filtersStr := strings.Join(clipFilters, ",")

		// 2. Applichiamo l'effetto overlay solo su specifiche clip (ogni 3 clip, indici 2, 5, 8...)
		if !noEffects && (idx+1)%3 == 0 && len(effects) > 0 {
			s.log.Info("stock pipeline effect applied",
				zap.Int("chunk_index", chunkIdx),
				zap.Int("clip_index", idx),
				zap.String("effect_file", filepath.Base(overlayFile)),
			)
			// Applichiamo prima i filtri video alla clip
			filterComplex.WriteString(fmt.Sprintf("[%d:v]%s[vtemp%d];", idx, filtersStr, idx))
			// Prepariamo l'overlay (dal file input overlay caricato all'indice `inputCount`)
			filterComplex.WriteString(fmt.Sprintf("[%d:v]scale=%d:%d,fps=%d,setsar=1,format=yuva420p,colorchannelmixer=aa=%f[effect%d];",
				inputCount, videoCfg.Width, videoCfg.Height, videoCfg.FPS, videoCfg.OverlayOpacity, idx))
			// Sovrapponiamo l'effetto solo su questa clip
			filterComplex.WriteString(fmt.Sprintf("[vtemp%d][effect%d]overlay=shortest=1[v%d];", idx, idx, idx))
		} else {
			// Nessun effetto per questa clip, la mandiamo direttamente al concat
			filterComplex.WriteString(fmt.Sprintf("[%d:v]%s[v%d];", idx, filtersStr, idx))
		}
	}

	// Concateniamo i flussi video risultanti [v0], [v1]... [vN-1]
	for idx := 0; idx < inputCount; idx++ {
		filterComplex.WriteString(fmt.Sprintf("[v%d]", idx))
	}
	filterComplex.WriteString(fmt.Sprintf("concat=n=%d:v=1:a=0[vfinal]", inputCount))

	args = append(args, "-filter_complex", filterComplex.String(), "-map", "[vfinal]")

	// Disable audio in output video when requested
	if noAudio {
		args = append(args, "-an")
	}

	// Per NVENC usiamo -cq invece di -crf (che non è supportato dall'encoder NVIDIA)
	if videoCfg.Codec == "h264_nvenc" {
		args = append(args,
			"-c:v", videoCfg.Codec,
			"-preset", videoCfg.Preset,
			"-rc", "constqp",
			"-qp", fmt.Sprintf("%d", videoCfg.CRF),
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart",
			outputPath,
		)
	} else {
		args = append(args,
			"-c:v", videoCfg.Codec,
			"-preset", videoCfg.Preset,
			"-crf", fmt.Sprintf("%d", videoCfg.CRF),
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart",
			outputPath,
		)
	}

	s.log.Info("rendering complex chunk with ffmpeg", zap.Int("chunk", chunkIdx), zap.String("output_path", outputPath))
	_, err = process.Run(ctx, s.ffmpegProc.Path(), args, process.Options{
		Timeout: 20 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("complex render chunk failed: %w", err)
	}

	s.log.Info("stock pipeline chunk complete", zap.Int("chunk", chunkIdx))
	return nil
}

// loadEffects scans the given directory for .mp4 overlay effect files.
func loadEffects(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("effects dir is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read effects dir %q: %w", dir, err)
	}
	var effects []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			effects = append(effects, filepath.Join(dir, e.Name()))
		}
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("no .mp4 effect files found in %s", dir)
	}
	return effects, nil
}
