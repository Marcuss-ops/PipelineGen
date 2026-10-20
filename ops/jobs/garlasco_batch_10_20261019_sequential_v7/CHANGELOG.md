# Garlasco: changelog del batch di 10 script

Batch completato il 19 ottobre 2026. I dieci payload e i relativi esiti riusciti conservati in questa cartella sono gli esempi di riferimento per i prossimi job Garlasco.

## Specifiche comuni

- Script lunghi circa 20–22 minuti, con circa 2.900–3.150 parole nei payload finali.
- Titoli e frasi in Playfair Display Italic, testo nero su sfondo bianco.
- Tre clip introduttive diverse per ogni video, selezionate tra quelle disponibili e con asset clip distinti.
- Stock condiviso tra i job; il caricamento su Drive resta responsabilità della pipeline.
- Ogni payload finale è stato completato con stato `SUCCEEDED`.

## Esempi conservati

| Script | Revisione finale | Job | Stato |
| --- | ---: | --- | --- |
| 01_cronologia | v8 | `job_1792422744138217083_c8f1b036` | `SUCCEEDED` |
| 02_procedimento | v15 | `job_1792435584749534650_75f50f69` | `SUCCEEDED` |
| 03_stasi_sentenza | v9 | `job_1792427488642782452_4344d151` | `SUCCEEDED` |
| 04_nuova_indagine | v11 | `job_1792429502476945054_88410407` | `SUCCEEDED` |
| 05_le_prove | v13 | `job_1792434233067818083_c34a2643` | `SUCCEEDED` |
| 06_pedali_ris | v13 | `job_1792434553872840768_c70fe4f2` | `SUCCEEDED` |
| 07_presunzione | v12 | `job_1792431577530467191_ff917aff` | `SUCCEEDED` |
| 08_fonti_media | v11 | `job_1792431956860243393_0bba2e07` | `SUCCEEDED` |
| 09_chiara_poggi | v11 | `job_1792432658289882234_0ee30f97` | `SUCCEEDED` |
| 10_cosa_sappiamo | v13 | `job_1792435035632098489_def74495` | `SUCCEEDED` |

I payload JSON sono nella cartella corrente; i dettagli completi degli esiti sono in `results/` accanto ai payload corrispondenti.

## Limite editoriale da rispettare nei prossimi job

Il target progettuale era circa 40 overlay per script, ma gli esiti finali di questo batch ne contengono da 13 a 36. Il batch è stato completato, ma non va usato per sostenere che tutti i video abbiano raggiunto quota 40. Per i prossimi job mantenere le tre intro distinte e verificare il conteggio reale degli overlay prima di considerarli conformi al target.

## Problema rilevato: short phrases e varietà delle animazioni

L'ispezione dei dieci `overlay_plan` salvati mostra che il piano non ha rispettato la richiesta di 10 frasi brevi e 10 lunghe per script:

- Sono state materializzate da 9 a 20 frasi totali per job. Otto job su dieci hanno zero o una frase sotto le sei parole; il primo ne ha 10 e il settimo ne ha una.
- Il budget richiesto è 20 frasi, ma in nove job il piano ne ha materializzate solo 9–11 (`phrase_overlay_budget.phrase_overlay_shortfall`: 9–11). Il budget è un tetto/richiesta, non una garanzia di quantità.
- Tutte le frasi materializzate in tutti e dieci i job usano `phrase_apple_clean_07_slide_up_soft`.

### Causa

`animation_counts.short_important_phrase: 10` regola la finestra di selezione delle animazioni per la corsia breve; non ordina al generatore di creare dieci overlay brevi. Anche `max_phrase_overlays: 20` stabilisce un limite superiore, non una quota garantita. La richiesta editoriale di “10 brevi e 10 lunghe” è nel testo del job, ma non è codificata come conteggio vincolante nel piano.

Inoltre ogni payload imposta `phrase_motions` a un array con il solo ID `phrase_apple_clean_07_slide_up_soft`. Questa scelta esplicita singleton prevale sulla varietà attesa da `phrase_motion_family: modern_product_v1`, quindi le frasi ripetono lo stesso ingresso. Il catalogo corrente consente alcuni accenti Brush morbidi, documentari e 3D per le frasi lunghe; i trattamenti Brush più vistosi e le animazioni typewriter sono invece ritirati e non vanno richiesti.

### Come correggere i prossimi job

1. Rendere esplicito e verificabile il numero di overlay brevi e lunghi nel piano: passare al generatore una quota separata per entrambe le corsie e assicurarsi che produca candidati brevi compatibili (1–5 parole) e lunghi (6 o più parole). Non usare `animation_counts` o `max_phrase_overlays` come se fossero quote di generazione.
2. Dopo la generazione, controllare il `overlay_plan` per conteggio e lunghezza delle frasi e rigenerare/correggere il job se una delle due quote manca; registrare anche il `phrase_overlay_shortfall` effettivo.
3. Rimuovere il `phrase_motions` singleton. Lasciare che `modern_product_v1` selezioni dal catalogo completo oppure fornire un pool certificato con più animazioni compatibili. Verificare poi i `motion_id` finali: le frasi lunghe devono ruotare tra famiglie disponibili, incluse Brush morbide, documentarie e 3D, non ripetere un solo ingresso.
4. Non inserire `typewriter` o i trattamenti Brush ritirati: scegliere solo ID presenti nel catalogo certificato. Per le frasi brevi usare esclusivamente ingressi compatibili con quella corsia.

Queste correzioni descrivono il requisito per i prossimi payload; non modificano retroattivamente i dieci risultati conservati.

### Correzione applicata

- `runner_overlay_prepare.go` ora conserva e seleziona separatamente candidati grounded da 2–5 parole e da 6–16 parole, alternando le due corsie entro il limite per scena. Se una corsia non ha abbastanza candidati, riempie gli slot residui con frasi valide dell'altra corsia.
- `runner_scene_materialization.go` non aggiunge più le frasi richieste ma assenti alla narrazione. Prima, un hint non associato a un segmento finiva nella prima scena: il canary ha mostrato 18 candidati brevi concentrati lì, con finestre sovrapposte che il selettore scartava. Gli hint ora restano input overlay e vengono accettati solo quando sono presenti nel testo finale della scena.
- Nei dieci payload di riferimento è stato rimosso il `phrase_motions` singleton. La famiglia `modern_product_v1` può così alimentare la rotazione automatica: frasi lunghe con varietà Apple, Brush morbida, documentaria e 3D; frasi brevi solo con ingressi compatibili.
- `overlay_group_limits.short_phrases: 10` e `long_phrases: 10` erano già presenti: ora i candidati possono arrivare a entrambe le quote e il piano applica i due massimali separatamente.

Il canary `02_procedimento.phrasefix` ha confermato la rotazione motion (12 frasi, 12 motion ID, con Apple, Brush, documentario e 3D), ma solo 2 frasi brevi: ha anche rivelato la concentrazione degli hint, corretta nel codice come sopra. Questo canary precede la correzione di materializzazione; i risultati storici non provano ancora la nuova quantità di frasi brevi. Il binario è stato ricompilato e riavviato con la correzione; un nuovo render completo non è stato lanciato.

### Verifica render completo 02_procedimento (19 ottobre 2026)

Il render `02_procedimento.phrasefix.finalcheck2` è terminato con `SUCCEEDED` dopo il retry del job, avviato perché un arresto del servizio aveva cancellato il primo tentativo. Run: `run_8ef7b828-76ef-46ea-a373-7a0b05cc070b`.

- Audio finale: 1.273.847 ms (circa 21:14); copione: 3.079 parole.
- Overlay: piano e render riportano 30 elementi, tutti `COMPLETED` (30/30); il requisito complessivo di 40 non è raggiunto.
- Gruppi effettivi: 18 frasi, 4 schede entità, 5 numeri/date e 3 mappe. Rispetto ai target restano 10 overlay totali, 2 frasi rispetto al budget di 20, 9 immagini rispetto al target di 13 e 5 schede entità rispetto alle 5 richieste (il piano ne ha 4).
- Delle 18 frasi, 10 hanno 2–5 parole e soddisfano la corsia breve; 8 hanno 6 parole e soddisfano la soglia minima della corsia lunga (6–16). Mancano 2 frasi lunghe rispetto al target di 10 e nessuna frase lunga supera le 6 parole. Il conteggio totale `phrase_overlay_budget` segnala 18/20.
- La selezione animazioni è invece varia: 18 frasi con 18 motion ID distinti, incluse due Brush, una documentaria e due 3D oltre agli ingressi Apple. Il render degli overlay è completato.
- Controllo visivo a campione dei segmenti MP4: la frase breve «morte di Chiara Poggi» appare correttamente in Playfair Display Italic nero su fondo bianco. Una frase Brush di sei parole mostra anche la sottolineatura color oro. La mappa Garlasco mostra la base cartografica e un punto; nel frame controllato non compare un'etichetta leggibile del luogo. Il file mappa si renderizza, ma la sua leggibilità va verificata prima di dichiarare corrette tutte le mappe.
- La revisione del testo narrato mostra inoltre alcuni innesti e frammenti grammaticalmente interrotti (per esempio «Qui, la Pro morte di Chiara Poggi»). Non considerare questo copione un esempio editoriale pulito finché non viene corretta e verificata la separazione tra narrazione e testo degli overlay.

Esito: la varietà motion e il completamento tecnico del render sono verificati; le quote brevi/lunghe, il totale di 40 overlay e la pulizia della narrazione non sono ancora conformi. Il risultato resta conservato come diagnostica, non come riferimento finale di qualità.

## Pulizia

Sono stati rimossi i job Garlasco precedenti fuori da questo batch (289 voci, 373 file), oltre ai payload e ai risultati delle revisioni precedenti o non riuscite del batch. Restano i dieci payload finali, i rispettivi risultati riusciti, questo changelog e il manifest.
