# TODO — revisione animazioni mappe e label (2026-10-17)

## Completato

- [x] `slow_approach` è l'unica animazione camera selezionabile a runtime.
- [x] Rimossi dal catalogo/runtime e dal registro condiviso: `simple_label`, `bracket_label`, `multi_locations`, `route_line`, `info_card`, `vertical_callout`, `global_context`.
- [x] Rimossi dal catalogo/runtime e dal registro condiviso: `silky_fade`, `rise_up`, `soft_tracking`, `title_top`, `caption_card`, `editorial_serif`, `gpin_bubble`, `bare_big`, `editorial_tag`, `ribbon`.
- [x] Aggiornati schema payload, commenti del contratto Go e aspettative dei test per i cataloghi ridotti.
- [x] Verificata la cartella Drive indicata; caricati lì i 10 MP4 singoli del catalogo finale. [Apri la cartella](https://drive.google.com/drive/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS).
- [x] Payload Garlasco r66 configurato con `region_zoom`, `slow_approach`, label `none`, overlay completi e massimo tre mappe.

## In sospeso

- [x] Il catalogo Python delle tre composizioni normali e delle sei scene regionali coincide con i dieci ID attivi del registro; il catalogo label è vuoto.
- [x] Pool casuale limitato alle tre composizioni normali; scene regionali solo esplicite.
- [x] La singola camera rimasta è `slow_approach`; label e composizione restano configurabili nel payload.
- [x] Ricompilato `bin/pipelinegen` e riavviato `pipelinegen.service`; `restart-verify` ha restituito `PASS`.


## Selezione finale richiesta (2026-10-17)

- [x] Catalogo runtime ridotto a 10 animazioni: sei scene Saudi/Middle East (`ed_saudi_base`, `ed_iran_glow`, `ed_yemen_fill`, `ed_yemen_markers`, `ed_hormuz_routes`, `ed_mocha_tactical`), tre mappe normali glow (`region_zoom`, `pulse_marker`, `spotlight`) e camera `slow_approach`.
- [x] Rimosso il catalogo delle label autonome; le mappe disegnano il proprio testo e `map_label_animation` è `none`.
- [x] r65 è fallito prima dei render perché il vecchio binario non accettava `map_label_animation=none`; nessun artifact da quel tentativo.
- [x] Job Garlasco r66 completato: `job_1792238746556077808_26fe5e8f`, 7 min circa, 15 scene/segmenti, piano overlay di 11 item (3 mappe), audio finale M4A da 7.5 MB; overlay MP4 GPU completato.
- [x] Rigenerati i 10 MP4 individuali del catalogo selezionato; tutti i render hanno passato il gate e sono stati caricati su Drive.
- [ ] Rivedere i clip mantenuti con l'utente e applicare il suo feedback estetico.
