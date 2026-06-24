# Regole operative obbligatorie

Queste regole valgono per ogni ticket.

## Git e branch

- Base consentita: solo `origin/main`.
- Una sola branch dedicata per ticket, indicata nel ticket.
- Non creare branch secondarie, stacked branch, branch di esperimento o branch di supporto.
- Non lavorare direttamente su `main`.
- Non fare push diretto su `main`.
- Non combinare due ticket nella stessa branch o PR.
- Prima di iniziare:
  ```bash
  git fetch origin
  git checkout main
  git pull --ff-only origin main
  git status -sb
  ```
- La working tree deve essere pulita.
- Creare la branch esatta indicata nel ticket.
- Prima del push:
  ```bash
  git fetch origin
  git rebase origin/main
  git status -sb
  git diff --check
  ```
- Dopo il push:
  ```bash
  git log -n 5 --oneline
  git status -sb
  ```
- Verificare che il commit remoto sia realmente aggiornato.

## Regole architetturali

- Cercare sempre il codice esistente prima di aggiungere nuovi tipi o package.
- Non duplicare registry, resolver, sampler, mapping o logica di routing.
- Ogni nuova astrazione deve entrare nel contratto canonico esistente.
- Nessun alias di compatibilità.
- Nessun wrapper pass-through lasciato “temporaneamente”.
- Nessun fallback silenzioso verso nomi, route, env var o payload vecchi.
- Nessun import `internal/infrastructure/*` da `internal/api`, `internal/application` o `internal/domain`, salvo ticket che sta eliminando una baseline già esistente e solo nei file esplicitamente autorizzati.
- SQL solo sotto `internal/infrastructure/database/**`.
- Adapter concreti costruiti solo in `internal/app`.
- API = trasporto; application = casi d'uso; domain = contratti; infrastructure = implementazioni.
- Non modificare comportamento pubblico non incluso nello scope.
- Non aggiornare baseline o allowlist per nascondere una violazione.
- Non aggiungere nuove feature.
- Non committare file generati, output, `node_modules`, `*.tsbuildinfo`, database o artefatti di test.

## Stop conditions

Fermarsi senza improvvisare se:

- il path indicato non esiste più su `origin/main`;
- il codice canonico esiste già in un altro package;
- la modifica richiede una nuova route, un nuovo payload o un nuovo job type;
- è necessario mantenere una compatibility layer;
- emergono due writer per lo stesso stato persistente;
- il ticket richiede file fuori dallo scope;
- i test dimostrano una dipendenza pubblica non documentata;
- il rebase introduce conflitti architetturali.

In questi casi documentare il blocco nella PR; non inventare una soluzione laterale.
