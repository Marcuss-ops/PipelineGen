# TICKET-LLM-OFFLOAD

**Stato:** APPROVATO 24/10/2026 — da implementare (può procedere in
parallelo agli altri ticket Drive-exit).
**Origine:** analisi Drive/servizi del 24/10/2026: la fase `generate`
(Ollama in CUDA, `OLLAMA_LLM_LIBRARY=cuda_v12`) gira sulla stessa GPU che
alimenta le lane di render e contende SM/VRAM con NVENC/Vulkan. Nessuna
misura ha ancora quantificato la competizione; la rimozione è un deploy, non
un progetto.
**Ordine consigliato:** indipendente; fare prima della scala orizzontale
così i pod RunPod nascono senza il carico LLM.

## Principio

La GPU dei worker serve a renderizzare. L'inferenza LLM (chiamate di
estrazione/annotazione su testo già scritto) vive su un servizio API hosted
(o su una macchina separata): la classe di strumento è «piccole chiamate
parallele», non «modello residente sulla GPU render». Resta valida anche
l'ottimizzazione più importante: se il `source_text` è dato verbatim, il
lavoro LLM va ridotto all'estrazione — meno chiamate prima ancora che più
veloci.

## Problema misurato

- `generate` è il collo più grosso end-to-end nel golden: **133–136 s** di
  wall con ~20 chiamate Ollama, mentre il testo narrativo è già fornito
  verbatim nel payload (istruzione «mantieni verbatim, non inventare»).
- Ollama gira in CUDA sulla stessa host delle lane render: la fase generate
  contende la GPU con il rendering (e nella configurazione attuale anche con
  TTS/audio), potenzialmente degradando le finestre render misurate
  (varianza v16/v18 non ancora spiegata).

## Cambio (fasi)

1. **Misurare prima:** profiling della competizione (VRAM/SM durante
   generate vs render) per quantificare il costo attuale — oggi è ipotesi
   motivata, non misura.
2. **Client LLM configurabile:** endpoint + API key (es. gateway OpenLLM o
   provider diretto tipo Groq/Together/Cerebras), fallback esplicito, senza
   dipendenze dalla GPU locale.
3. **Ridurre le chiamate:** le sezioni a testo dato passano da
   riformattazione generativa a estrazione/annotazione (deterministica o
   chiamate piccole parallele); misurare il wall generate prima/dopo.
4. **Rimozione di Ollama dal host render** (service drop-in rimosso): la GPU
   resta a disposizione delle lane.

## Gate di accettazione

- [ ] Durante una run render: zero processi di inferenza LLM sul host/GPU
      dei worker (verificato da ps/nvidia-smi durante la run).
- [ ] Wall `generate` misurato prima/dopo su uno stesso piano golden:
      obiettivo ≤ 30% del baseline 133 s (chiamate parallele + meno
      chiamate); qualsiasi regressione di qualità del piano è dichiarata.
- [ ] Le chiamate residue sono solo estrazione/annotazione: nessun passaggio
      riformatta testo già fornito (verificabile sul payload golden).

## Dipendenze e rischi

- Dipendenza esterna: servizio LLM con API key (costo a consumo, basso per
  chiamate di estrazione). Il fallback locale (Ollama su macchina separata)
  deve restare configurabile per non bloccare le run se l'API è giù.
- La qualità dell'estrazione va verificata sul gold semantico (dipendenza
  dal gold T01/T05 già aperta sul fronte Rust).
