#!/usr/bin/env python3
"""Build the LONG-clip Crepet selection (5-10 minute segments).

Second editorial pass: the operator asked for segments in the 5-10 minute
range instead of the 56s cuts of the first pass. Same canonical
POST /api/clips/process contract (ExtractRequest), same destination folder,
same deterministic yt_<videoID>_<start>_<end>_v1 identity (so no collision
with the first-pass clips).

Duration gate: the canonical SegmentPolicy default is 4-60s, so the server
MUST run with VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS >= 600 for these
segments to pass Step 1 (otherwise FailureCodeDurationOutOfRange).

Outputs, under data/tclip/crepet/long/:
  crepet_long_clips.json     the structured deliverable
  payload_<videoID>.json     one ExtractRequest per video
"""
from __future__ import annotations

import json
import re
from pathlib import Path

SUBS = Path("/tmp/crepet_subs")
OUT = Path(__file__).resolve().parents[2] / "data" / "tclip" / "crepet" / "long"

INNER_TAG = re.compile(r"<[^>]+>")
TS = re.compile(r"(\d{1,2}):(\d{2}):(\d{2})[.,](\d{1,3})")
FOLDER_ID = "1IrrIv7UF5ifgjL6ShBdO22N2P42gdhRP"

VIDEOS = [
    {
        "video_id": "eFvWKqU7KL4",
        "title": "Luca Sommi intervista Paolo Crepet",
        "channel": "Luca Sommi",
        "duration": 4533,
        "blocks": [
            (390, 900, "Social, fertilità e confini: la generazione degli smartphone",
             "social coprifuoco minori smartphone iPhone 2007 Steve Jobs crollo fertilità affettività fuga cervelli 230000 giovani emigrazione Confindustria Sorbona confini aberrazione",
             "Dalla proposta inglese di vietare i social ai sedicenni al crollo della fertilità dal 2006: la tecnologia ha eroso la capacità di costruire legami. E i giovani talenti che se ne vanno dimostrano che i confini sono segni geografici senza senso."),
            (1000, 1480, "Empatia, autorità e autonomia: ciò che togliamo ai ragazzi",
             "empatia ospedale infermiera pantofole rosse robot umanità autorità professore genitori scuola meritocrazia soldi zainetto nonni Pande autonomia 16 anni fragilità comodità",
             "Da paziente cardiopatico vuole un essere umano che gli parli, non un robot; a scuola i genitori litigano col professore perché il denaro è diventato la misura del valore; e i ragazzi accompagnati fino a sedici anni perdono il diritto di imparare da soli."),
            (2070, 2670, "Internet, indifferenza e l'algoritmo che fa il prete",
             "internet indifferenza TikTok scroll dopamina dipendenza eroina immagini Roma Ronaldo bombardamento ChatGPT ragazza sentimenti algoritmo prete di campagna prostrazione emotiva",
             "Internet amplifica i divari e lo scroll abbassa la soglia dell'attenzione fino a rendere indifferenti davanti alla morte come a un gol; una ragazza lasciata chiede di notte a ChatGPT cosa fare e l'algoritmo le risponde come un prete di campagna."),
            (3090, 3690, "«Think different» tradito e la vita che deve finire in rosso",
             "Steve Jobs Think different omologazione Apple Biennale Cina robot Malévic autenticità business finire in rosso dare più di quanto si riceve David Hockney re Carlo",
             "Steve Jobs prometteva strumenti per coltivare la diversità e ha prodotto l'omologazione più assoluta; ma la vita resta l'unico business che deve chiudere in rosso, perché bisogna dare più di ciò che si è ricevuto."),
        ],
    },
    {
        "video_id": "wg--mrIqCXo",
        "title": "RISVEGLIARE LE COSCIENZE! PAOLO CREPET passa dal BSMT!",
        "channel": "The BSMT by Gianluca Gazzoli",
        "duration": 6145,
        "blocks": [
            (880, 1480, "Basaglia, il disagio psichico e l'arte di rialzarsi",
             "Franco Basaglia 2 agosto 1980 stazione Bologna manicomio psichiatria Martin Luther King diritti ultimi 120000 internati Oliviero Toscani disagio psichico comunicabile depressione famiglia diagnosi errore rialzarsi autonomia",
             "L'ultimo saluto a Basaglia il 2 agosto 1980, il «nostro Martin Luther King» che aprì i manicomi; poi il disagio che non è trasmissibile ma comunicabile, e la convinzione che dalla propria caduta si esce con le proprie gambe."),
            (1600, 2200, "«Badati», «qualcosa è andato storto» e la troppa biada",
             "badati nonna autorità educazione bicicletta autonomia Sam Altman intelligenza artificiale qualcosa è andato storto consapevolezza troppa biada cavalli giovani adolescenti albero quercia cena famiglia",
             "Una sola parola della nonna, «badati», vale più di ogni soccorso; ammettere che «qualcosa è andato storto» è la base dell'ottimismo; e dare troppo ai ragazzi li rende molli come i cavalli ammalati dalla troppa biada."),
            (2440, 3040, "L'eredità, il cobra nell'armadio e il rifiuto della comodità",
             "eredità casa Airbnb comfort zone attica detestare la vita India Trivandrum cobra armadio pitone rischio viaggio pericolo non mettersi comodi",
             "Mettere una casa ereditata su Airbnb per non lavorare è l'apoteosi della comfort zone; a Trivandrum un cobra nell'armadio ricorda che la vita vera è convivere con il rischio, non con il comfort."),
            (4480, 5080, "Instagram e il successo: la perfezione che genera disagio",
             "Instagram perfezione visibilità filtro faccia disagio commissione Senato Stati Uniti competizione estetica successo participio passato Vittorio Gassman durare Rolling Stones",
             "Instagram insegna che bisogna essere perfetti per esistere e riduce una persona alla propria faccia; il successo è un participio passato, quindi la vera difficoltà non è arrivare ma durare."),
            (5545, 6145, "Cercare i ribelli di ogni generazione",
             "ribelli Marlon Brando Oscar rifiutato Bob Dylan Nobel non ritirato ottimismo nuove generazioni tecnologia intelligenza artificiale felicità mondo grande Occidente",
             "I ribelli ci sono sempre stati e ci saranno sempre: Brando che rifiuta l'Oscar, Dylan che non ritira il Nobel. Il mondo è molto largo e i giovani hanno una straordinaria possibilità tecnologica da mettere al servizio della felicità."),
        ],
    },
    {
        "video_id": "PD9X7oDwdYQ",
        "title": "La Potente Ragione per Cui Devi Curarti Ogni Giorno | Paolo Crepet",
        "channel": "Voci Interiori",
        "duration": 2240,
        "blocks": [
            (0, 480, "La cura quotidiana come rispetto di sé",
             "cura di sé abitudini riti quotidiani rispetto dignità postura interiore barba profumo abito paesaggio mentale identità presenza",
             "Prendersi cura di sé non è vanità né narcisismo ma rispetto: i piccoli riti quotidiani danno forma all'identità e dicono alla mente «io mi rispetto»."),
            (480, 1000, "Trascuratezza, disciplina e approvazione esterna",
             "trascuratezza causa tristezza stress narrativa distorta disciplina piccoli rituali muscolo invisibile dipendenza approvazione esterna manipolazioni autonomia valore",
             "La trascuratezza non è solo l'effetto della tristezza ma può esserne la causa; la disciplina nasce dai piccoli rituali ripetuti e riduce la dipendenza dall'approvazione altrui."),
            (1000, 1500, "Tempo, presenza e dispersione dell'energia",
             "tempo nemico soglia minuti energia lucidità presenza gestione energia narrazione distorta resilienza rispetto della propria storia priorità",
             "Quando dedichi pochi minuti al giorno a te stesso il tempo smette di essere un nemico e diventa uno spazio modellabile; la cura evita la dispersione di energia e costruisce resilienza."),
            (1500, 1860, "Autostima silenziosa, corpo e confini invisibili",
             "autostima silenziosa platea coerenza corpo interlocutore segnali esaurimento confini invisibili relazioni equilibrate reciprocità dipendenze emotive",
             "L'autostima vera non ha bisogno di una platea; il corpo torna a essere un interlocutore e la cura stabilisce confini invisibili che selezionano le relazioni sane da quelle disfunzionali."),
            (1860, 2240, "Solitudine fertile, gratitudine concreta e casa interiore",
             "solitudine fertile maturità emotiva gratitudine concreta senso della realtà casa interiore appartenenza a se stessi gratitudine",
             "Chi impara a curarsi trasforma la solitudine in uno spazio fertile e sviluppa una gratitudine concreta: la cura quotidiana diventa una casa interiore che accompagna ovunque."),
        ],
    },
    {
        "video_id": "cHCQTHdAkOM",
        "title": "PAOLO CREPET: DALLA CASA NEL BOSCO ALL'INTELLIGENZA ARTIFICIALE",
        "channel": "SUPERNOVA - Ale Cattelan",
        "duration": 4857,
        "blocks": [
            (0, 510, "Iperaccudimento, la ribellione gentile e il fazzoletto in stazione",
             "iperaccudimento mamma figlio ventenne robot flebo cinico romantico ribellione soft ironia fazzoletto stazione madre pantera adolescentizzazione",
             "Il ventenne che non ha mai fatto nulla «perché ci ha pensato la mamma», la scelta di passare da cinico a romantico con una ribellione gentile, e la madre «pronta come una pantera» col fazzoletto in stazione."),
            (690, 1230, "ChatGPT come un prete di campagna e i genitori giovanilisti",
             "ChatGPT ragazza sentimenti prete di campagna consenso algoritmi genitori giovanilisti stessa musica dei figli followers adolescentizzazione adulti",
             "Una ragazza chiede di notte a ChatGPT cosa fare col fidanzato e l'algoritmo risponde come un prete di campagna: è programmato per coccolare, non per dire la verità. Intanto gli adulti si adolescentizzano cercando consenso."),
            (1650, 2100, "Mike, la bambina e l'umanità che nessun robot sostituisce",
             "Mike Harley Davidson volontario oncologia pediatrica bambina favole papà fino a quando muoio umanità robot assistenza giocare perdere nascondino frustrazione",
             "Mike, volontario in oncologia pediatrica, e la bambina che gli chiede di fare il papà «fino a quando muoio»: l'umanità che nessun robot da assistenza potrà sostituire. E giocare vuol dire imparare a perdere."),
            (2250, 2850, "Le carte norvegesi, Mario Lodi e l'eremita al liceo",
             "Norvegia tablet scuola digitale sottotitoli concentrazione Mario Lodi Alfredo sacchetti colori lavagna Costituzione diritti bambini eremita montagna liceo La Spezia silenzio diversità",
             "L'esperimento norvegese che toglie i tablet perché la coorte non legge più i sottotitoli; Mario Lodi e il bambino che rifiuta di copiare la lavagna; l'eremita portato in un liceo che folgora i ragazzi col silenzio."),
            (3630, 4216, "Basaglia al muro di Berlino, l'angelo veneziano e l'albergo Angst",
             "Basaglia Berlino porta di Brandeburgo muro manicomi muri invisibili Venezia baretto angelo viaggiare disincanta Ungaretti scrivere malinconia Bordighera albergo Angst dentifricio",
             "Davanti al muro di Berlino Basaglia avverte che quando se ne abbatte uno qualcuno ne costruisce uno più alto e invisibile; nel baretto un signore antichissimo dice «vara che a viaggiare desincantar» e Crepet ricomincia a scrivere."),
        ],
    },
    {
        "video_id": "MHxuu0-TGJk",
        "title": "Il Fienile Podcast - Episodio 9 - \"Il maestro di vita\" con Paolo Crepet",
        "channel": "il fienile",
        "duration": 4227,
        "blocks": [
            (0, 570, "Peggy Guggenheim, il nonno pittore e Modigliani",
             "Peggy Guggenheim Jackson Pollock Venezia tè affumicato nonno Angelo Maria pittore Accademia Modigliani compagno di corso ceramista Montessori infanzia",
             "A diciannove anni Crepet beve un tè affumicato con Peggy Guggenheim davanti a un Pollock; poi la scoperta che il nonno Angelo Maria era compagno di corso di Modigliani, e il colpo di testa alla Montessori."),
            (710, 1170, "Terzian, la TAC di Lennon, l'eroina a Verona e la malattia mentale",
             "Terzian neurochirurgo severità orologio Padova competenze emisferiche TAC John Lennon Beatles Abbey Road eroina Verona Piazza Dante anni Settanta Basaglia padre malattia mentale organo sociologia",
             "Il professor Terzian e la sua severità, la TAC il cui primo prototipo fu finanziato da John Lennon, l'eroina che a Verona uccise una generazione e la domanda del padre medico: «la malattia mentale in quale organo sta?»."),
            (1230, 1680, "L'odore dei manicomi, la bistecca all'OMS e i maestri",
             "Basaglia manicomi odore dignità 120000 internati bistecca capo psichiatria OMS Ginevra India Praga Toronto padre non ti ha mai detto bravo maestri ciliegina torta pianoforte",
             "Basaglia aprì i manicomi perché non sopportava quell'odore; una bistecca porta Crepet all'OMS e ai manicomi di mezzo mondo; e dal funerale del padre arriva la lezione: i maestri lasciano la ciliegina, la torta devi farla tu."),
            (1840, 2366, "India, Praga, Jan Palach e il reportage che si mette allo stesso livello",
             "India Chandigarh Le Corbusier autista otto figli primo anno di vita Cecoslovacchia Breznev libri sottolineati Jan Palach Praga 1968 carri armati Ramon de Pardon Magnum manicomio Trieste carte",
             "In India l'autista con otto figli insegna il rispetto per la vita; a Praga i libri di Breznev stampati già sottolineati e il sacrificio di Jan Palach; e Ramon de Pardon che al manicomio di Trieste si mette a giocare a carte invece di usare la macchina fotografica."),
        ],
    },
]


def parse_vtt(path: Path):
    cues = []
    lines = path.read_text(errors="ignore").splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        if "-->" in line:
            a, b = line.split("-->", 1)
            m1 = TS.search(a)
            m2 = TS.search(b)
            if m1:
                s = int(m1.group(1)) * 3600 + int(m1.group(2)) * 60 + int(m1.group(3))
                s = s * 1000 + int(m1.group(4).ljust(3, "0"))
                e = s
                if m2:
                    t = int(m2.group(1)) * 3600 + int(m2.group(2)) * 60 + int(m2.group(3))
                    e = t * 1000 + int(m2.group(4).ljust(3, "0"))
                parts = []
                i += 1
                while i < len(lines) and lines[i].strip():
                    parts.append(INNER_TAG.sub("", lines[i]))
                    i += 1
                txt = " ".join(parts).strip()
                if txt:
                    cues.append((s, e, txt))
        i += 1
    return cues


def dedup_append(acc, words, max_overlap=120):
    n = min(len(words), len(acc), max_overlap)
    for L in range(n, 0, -1):
        if acc[-L:] == words[:L]:
            acc.extend(words[L:])
            return
    acc.extend(words)


def window_text(cues, start_s, end_s):
    acc = []
    for s, e, txt in cues:
        if e <= start_s * 1000 or s >= end_s * 1000:
            continue
        dedup_append(acc, txt.split())
    return " ".join(acc).strip()


def mmss(total_sec: int) -> str:
    return f"{total_sec // 60:02d}:{total_sec % 60:02d}"


def hms(total_sec: int) -> str:
    return f"{total_sec // 3600:02d}:{(total_sec % 3600) // 60:02d}:{total_sec % 60:02d}"


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    deliverable = []
    for v in VIDEOS:
        vid = v["video_id"]
        vtt = SUBS / f"{vid}.it-orig.vtt"
        if not vtt.is_file():
            vtt = SUBS / f"{vid}.it.vtt"
        cues = parse_vtt(vtt)
        clips_out = []
        segments = []
        for idx, (st, en, topic, ents, summ) in enumerate(v["blocks"], 1):
            dur = en - st
            assert 300 <= dur <= 600, f"{vid} block {idx} duration {dur}s outside 5-10 min"
            assert en <= v["duration"], f"{vid} block {idx} ends past the video"
            tr = window_text(cues, st, en)
            clips_out.append({
                "clip_id": idx,
                "start": mmss(st),
                "end": mmss(en),
                "topic": topic,
                "vector_description": ents,
                "summary": summ,
            })
            segments.append({
                "start": hms(st),
                "end": hms(en),
                "name": topic,
                "category": "celebrity_interview",
                "source_title": v["title"],
                "source_channel": v["channel"],
                "topics": [topic] + [t.strip() for t in ents.split()[:10]],
                "tags": [t for t in ents.split()[:14]],
                "summary": summ,
                "texts": [{
                    "language_code": "it",
                    "source_language_code": "it",
                    "transcript": tr,
                    "description": summ,
                    "summary": summ,
                    "title": topic,
                    "source_type": "provided",
                    "is_original": True,
                }],
            })
        deliverable.append({
            "video_url": f"https://www.youtube.com/watch?v={vid}",
            "title": v["title"],
            "clips": clips_out,
        })
        payload = {
            "url": f"https://www.youtube.com/watch?v={vid}",
            "strategy": "verify",
            "concurrency": 2,
            "keep_audio": True,
            "write_summary": True,
            "destination": {
                "folder_id": FOLDER_ID,
                "create_subfolder": False,
            },
            "segments": segments,
        }
        (OUT / f"payload_{vid}.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2))
        print(f"{vid}: {len(segments)} long clips ({sum(b[1] - b[0] for b in v['blocks'])}s total), payload written")
    (OUT / "crepet_long_clips.json").write_text(json.dumps(deliverable, ensure_ascii=False, indent=2))
    print(f"deliverable written -> {OUT / 'crepet_long_clips.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
