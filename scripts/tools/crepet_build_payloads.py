#!/usr/bin/env python3
"""Build the Crepet clip selection (deliverable JSON + /api/clips/process payloads).

Editorial selection of the most salient moments from 5 Paolo Crepet interviews.
Segments are kept <= 58s so they pass the canonical SegmentPolicy (4-60s) even
if the deployment runs the 60s bound instead of the 240s override.

Outputs, under data/tclip/crepet/:
  crepet_clips.json          the structured deliverable (MM:SS, total minutes)
  payload_<videoID>.json     one ExtractRequest per video (one download, N cuts)
"""
from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent
SUBS = Path("/tmp/crepet_subs")
OUT = Path(__file__).resolve().parents[2] / "data" / "tclip" / "crepet"

INNER_TAG = re.compile(r"<[^>]+>")
TS = re.compile(r"(\d{1,2}):(\d{2}):(\d{2})[.,](\d{1,3})")

VIDEOS = [
    {
        "video_id": "eFvWKqU7KL4",
        "title": "Luca Sommi intervista Paolo Crepet",
        "channel": "Luca Sommi",
        "clips": [
            # (start_sec, end_sec, topic, entity_dense, summary)
            (420, 476, "Social e coprifuoco: \"guarderò il muro\"",
             "social coprifuoco 16 anni Starmer Inghilterra ragazza inglese cellulare amici genitori dipendenza guarderò il muro",
             "La ragazza inglese interpellata sulla proposta di vietare i social ai minori: se non potrà usare il telefono non le resterà che \"guardare il muro\", perché non saprebbe più parlare con amici e genitori."),
            (560, 616, "Più smartphone, meno figli: crolla la fertilità e l'affettività",
             "fertilità affettività iPhone 2007 Steve Jobs diffusione telefoni crollo demografico inversamente proporzionale relazioni",
             "Il rapporto statistico tra diffusione degli iPhone dal 2006 e calo della fertilità: non crolla solo la natalità, crolla la capacità di costruire legami affettivi."),
            (790, 846, "La fuga dei cervelli e i confini come aberrazione",
             "fuga cervelli Confindustria giovani emigrazione 230000 ragazzi 2010 Paganini Verdi Giordano Bruno Sorbona confini aberrazioni Amsterdam Londra Dublino",
             "Se i giovani con talento se ne vanno, il problema non è la vergogna ma le condizioni che li spingono via: i confini sono segni geografici senza senso, come dimostra la storia degli artisti europei."),
            (1035, 1091, "Empatia contro il robot: l'infermiera e le pantofole rosse",
             "empatia ospedale infermiera pantofole rosse comunista cardiopatico robot ospedale cinese umanità cura notte",
             "Da paziente cardiopatico di notte, ciò che vuole non è un robot ma un essere umano che si sieda e gli parli: l'empatia non è sostituibile e le pantofole rosse scatenano il sospetto di comunismo."),
            (1145, 1201, "Perché i genitori litigano con il professore",
             "autorità professore genitori scuola soldi superiorità meritocrazia egualitarismo democrazia diritti Pollock decreti delegati",
             "Il rovesciamento dell'autorità scolastica: quando i soldi sono diventati la misura del valore, i genitori hanno disconosciuto il professore; democrazia significa pari diritti, non uguaglianza di merito."),
            (1390, 1446, "Lo zainetto, le Pande dei nonni e il diritto all'autonomia",
             "zainetto scuola nonni Pande traffico liceo 16 anni autonomia comodità fragilità Viola portico corteggiamento",
             "I ragazzi portati a scuola fino a sedici anni vengono resi fragili: il diritto di aspettare \"Viola\" sotto il portico è una palestra di vita, l'opposto di un mondo fondato sulla comodità."),
            (2110, 2166, "Internet e la costruzione dell'indifferenza",
             "internet indifferenza TikTok scroll dopamina dipendenza eroina immagini Roma Ronaldo bombardamento senso delle immagini",
             "Internet non democratizza ma amplifica i divari, e il meccanismo dopaminergico dello scroll — come l'eroina — abitua a passare da un gol alla morte alla guerra annullando ogni senso dell'immagine."),
            (2590, 2646, "La ragazza che chiede a ChatGPT del fidanzato",
             "ChatGPT ragazza sentimenti algoritmo prete di campagna psicoterapeuta fidanzato vergogna amica baretto algoritmo 24 ore",
             "Una ragazza, lasciata, chiede di notte a ChatGPT cosa fare: l'algoritmo le risponde come un prete di campagna degli anni Sessanta. È prostrazione emotiva, non terapia."),
            (3130, 3186, "Il tradimento di \"Think different\"",
             "Steve Jobs Think different omologazione Apple computer autonomia pensiero biennale Cina robot Malévic quadrato nero avanguardia",
             "Steve Jobs prometteva strumenti per coltivare la diversità, oggi il risultato è l'omologazione più assoluta: il padiglione cinese alla Biennale pieno di robot è il rovescio del quadrato nero di Malévic."),
            (3620, 3676, "La vita è l'unico business che deve finire in rosso",
             "vita business finire in rosso dare più di quanto si riceve ribelli ortodossia Giordano Bruno David Hockney ritratto re Carlo",
             "\"La vita è l'unico business che deve finire in rosso\": bisogna dare di più di ciò che si è ricevuto. Il pittore David che rifiuta il ritratto al re d'Inghilterra perché \"faccio i ritratti solo alle persone che mi sono simpatiche\"."),
        ],
    },
    {
        "video_id": "wg--mrIqCXo",
        "title": "RISVEGLIARE LE COSCIENZE! PAOLO CREPET passa dal BSMT!",
        "channel": "The BSMT by Gianluca Gazzoli",
        "clips": [
            (910, 966, "L'ultima volta che vidi Basaglia",
             "Franco Basaglia 2 agosto 1980 stazione Bologna esplosione treno secondo padre manicomio psichiatria morto 1980",
             "Il racconto dell'ultima volta in cui Crepet può salutare Franco Basaglia: il 2 agosto 1980 il treno che doveva portarlo a Venezia non parte perché la stazione di Bologna viene colpita dalla bomba."),
            (1030, 1086, "Basaglia, \"il nostro Martin Luther King\"",
             "Basaglia Martin Luther King diritti ultimi manicomi 120000 internati camicia di forza Oliviero Toscani eredità più grande del lavoro",
             "Chi era davvero Basaglia: non solo psichiatra ma uomo che ha lottato per i diritti degli ultimi, capace di aprire i manicomi. Come Toscani, ha lasciato un'eredità più grande del suo lavoro."),
            (1330, 1386, "Il disagio psichico non è trasmissibile, ma è comunicabile",
             "disagio psichico depressione famiglia trasmissione comunicazione tabù psicologi liceo sovradimensionamento diagnosi",
             "Il disagio non si trasmette come una malattia infettiva ma si respira in famiglia; e oggi c'è un sovradimensionamento: \"tutti sono diventati disagiati\", troppi psicologi e troppe scorciatoie."),
            (1400, 1456, "Entrare e uscire dal proprio errore",
             "errore cadere tirarsi su aiuto autonomia autostima trappola forze proprie dipendenza riscatto",
             "Credere nella possibilità di uscire da un proprio errore: se appena cadi qualcuno ti dà subito la mano, non impari a cavartela da solo. La vera forza è rialzarsi con le proprie gambe."),
            (1630, 1686, "\"Badati\": la parola che cambia la vita",
             "nonna badati badate autorità educazione bicicletta ghiaia regolarsi autonomia responsabilità",
             "La nonna di Crepet, davanti alle sue marachelle, diceva una sola parola: \"badati\". Non soccorri, non sostituisci: metti l'altro nella condizione di badare a se stesso."),
            (1750, 1806, "\"Qualcosa è andato storto\"",
             "Sam Altman intelligenza artificiale qualcosa è andato storto consapevolezza cambiare maglione doccia responsabilità altri",
             "La frase di chi ha inventato l'intelligenza artificiale — \"qualcosa è andato storto\" — diventa la base dell'ottimismo: riconoscere che qualcosa non funziona è il primo passo per ricominciare, senza dare la colpa agli altri."),
            (1880, 1936, "Troppa biada ammala i cavalli",
             "troppa biada cavalli giovani adolescenti tutto dato niente cercare molle non cresci forte contadino albero quercia frutto figli",
             "Il proverbio francese che Crepet si è annotato: dare troppo ai ragazzi li rende molli. Nessun contadino pianta un albero da frutto sotto una quercia, eppure noi teniamo i figli all'ombra della nostra grande quercia."),
            (2170, 2226, "La cena di famiglia: cosa volete fare della vostra vita",
             "cena famiglia riunita dire cosa volete fare nella vita scenari nonna sviene applausi banalità segno partire cercare",
             "Quando i ragazzi dicono alla famiglia cosa vogliono fare davvero, ci sono due scenari: che tutti applaudano una banalità, o che la nonna svenga perché hai detto qualcosa di autentico. Solo il secondo lascia un segno."),
            (2470, 2526, "L'eredità e l'idea orrenda di \"metto su Airbnb\"",
             "eredità casa campagna appartamento Airbnb comfort zone attica Lurid mettersi comodi detestare la vita rischio",
             "L'idea orrenda di chi eredita una casa e la mette su Airbnb per non lavorare: è l'apoteosi della comfort zone, un modo per detestare la vita al punto da trasformarla in una \"comfort zoo\"."),
            (2590, 2646, "Il cobra nell'armadio",
             "India Trivandrum cobra armadio camera pitone strangolatore comfort zone rischio viaggio dormire pericolo",
             "A Trivandrum, in India, il ragazzo che dorme nella stanza di fronte segnala un cobra dentro l'armadio: la vita vera non è comfort zone, è imparare a convivere con il rischio e con l'imprevisto."),
            (4510, 4566, "Instagram insegna la perfezione: un delitto",
             "Instagram perfezione visibilità filtro faccia cervello disagio commissione Senato Stati Uniti concorso bellezza inganno",
             "Instagram ti insegna che devi essere perfetto per esistere: ridurre una persona alla propria faccia è offensivo, e la competizione estetica permanente genera disagio che i filtri non cancellano."),
            (4870, 4926, "Il successo è un participio passato",
             "successo participio passato già accaduto Vittorio Gassman voce carriera durata Rolling Stones non correre dietro",
             "\"Il successo è un participio passato\": una volta che arrivi è già accaduto, quindi non ha senso correrci dietro. La vera difficoltà è durare, e per durare bisogna saper fare le cose, non una cosa."),
            (5830, 5886, "Cercare i ribelli di ogni generazione",
             "ribelli generazione Marlon Brando Oscar rifiutato Bob Dylan Nobel non ritirato ottimismo giovani mondo grande" ,
             "I ribelli ci sono sempre stati e ci saranno sempre, anche se oggi fanno più fatica: Brando che rifiuta l'Oscar, Bob Dylan che non ritira il Nobel. Il mondo è molto largo e non si esaurisce nell'Occidente."),
        ],
    },
    {
        "video_id": "PD9X7oDwdYQ",
        "title": "La Potente Ragione per Cui Devi Curarti Ogni Giorno | Paolo Crepet",
        "channel": "Voci Interiori",
        "clips": [
            (30, 86, "Curarsi non è vanità: è rispetto di sé",
             "cura di sé abitudini rispetto dignità piccolo riti quotidiani autostima presenza identità valore",
             "Prendersi cura di sé ogni giorno non è estetica né narcisismo: è dire \"io mi rispetto\". Trascurare questi gesti fa lentamente perdere la percezione del proprio valore."),
            (140, 196, "La cura come postura interiore",
             "postura interiore barba profumo abito paesaggio mentale consapevolezza corpo mente ponte dentro fuori dignità",
             "Sistemare la barba, scegliere un abito, ordinare lo spazio non è vanità: sono azioni che riorganizzano il paesaggio mentale e fanno da ponte tra ciò che senti dentro e ciò che proietti nel mondo."),
            (320, 376, "Non vivere in modalità sopravvivenza",
             "modalità sopravvivenza giornate inseguire spazio mentale fermarsi riorientare attenzione inerzia centro della vita",
             "Molte persone vivono in modalità sopravvivenza, sempre in ritardo su se stesse: la cura è fermarsi e dire \"adesso rioriento la mia attenzione\", interrompendo l'inerzia e tornando al centro della propria vita."),
            (490, 546, "La trascuratezza come causa, non solo effetto",
             "trascuratezza causa tristezza stress narrativa distorta percezione relazioni lavoro consapevolezza spirale merito",
             "Si crede che la trascuratezza sia solo l'effetto della tristezza, ma può esserne la causa: la mente costruisce una narrativa distorta di te e finisci per accettare relazioni e lavori che non ti valorizzano."),
            (610, 666, "La disciplina nasce dai piccoli rituali",
             "disciplina piccoli rituali abitudini alzarsi lavarsi vestito ordine muscolo invisibile fermezza obiettivi grandi progetti",
             "La disciplina non nasce da grandi progetti ma da piccoli rituali ripetuti: alzarsi, lavarsi, scegliere un vestito con intenzione costruiscono un muscolo invisibile che regge le scelte più complesse."),
            (860, 916, "Meno dipendenza dall'approvazione esterna",
             "dipendenza approvazione esterna conferma notati riconosciuti autonomia stabilità emotiva manipolazioni relazioni sbilanciate valore",
             "Chi non si prende cura di sé cerca negli altri la conferma che non trova dentro; costruendo il proprio valore con gesti concreti si recupera un'autonomia profonda e si diventa meno vulnerabili alle manipolazioni."),
            (1100, 1156, "Il tempo smette di essere un nemico",
             "tempo nemico rincorsa soglia minuti energia lucidità ritmo quotidiano temporalità presenza spazio modellare",
             "Quando dedichi anche pochi minuti al giorno a te stesso, il tempo smette di essere un nemico e diventa uno spazio che puoi modellare: quei minuti diventano una soglia in cui riorganizzi l'energia."),
            (1630, 1686, "L'autostima silenziosa",
             "autostima silenziosa approvazione complimenti riconoscimenti platea valore solidità coerenza libertà soglia gratitudine",
             "L'autostima vera non dipende dall'approvazione esterna ma cresce quando ti tratti con rispetto: costruita su gesti quotidiani diventa solida, e non hai più bisogno di una platea per sentirti degno."),
            (1810, 1866, "La solitudine come spazio fertile",
             "solitudine mancanza vuoto ascoltarsi maturità emotiva presenza degli altri territorio fertile energia riempire bisogni",
             "Chi teme la solitudine la associa alla mancanza; chi impara a curarsi la trasforma in uno spazio in cui ascoltarsi senza interferenze, e questa capacità di stare con se stessi è una delle forme più potenti di maturità emotiva."),
            (1990, 2046, "Confini invisibili e relazioni equilibrate",
             "confini invisibili valore relazioni equilibrate reciprocità ascolto comportamenti dinamiche disfunzionali connessioni umane selezionare",
             "Curarsi ogni giorno significa stabilire confini invisibili ma potenti: non adattarsi a dinamiche che feriscono e selezionare relazioni fondate su reciprocità e ascolto, dove il movimento va in entrambe le direzioni."),
            (2180, 2236, "Gratitudine concreta",
             "gratitudine concreta affetto opportunità calma progresso senso della realtà mancanza forza problemi terreno",
             "La cura quotidiana affina il senso della realtà: smetti di vivere in una narrativa di mancanza e riconosci ciò che ti sostiene. È una gratitudine concreta che ti fa affrontare i problemi da un terreno di forza, non di fragilità."),
        ],
    },
    {
        "video_id": "cHCQTHdAkOM",
        "title": "PAOLO CREPET: DALLA CASA NEL BOSCO ALL'INTELLIGENZA ARTIFICIALE",
        "channel": "SUPERNOVA - Ale Cattelan",
        "clips": [
            (10, 66, "\"Ci ha pensato la mamma\": l'iperaccudimento",
             "mamma figlio ventenne vita iperaccudimento robot flebo invecchiare indipendenza famiglia bambini soli diversi",
             "Il ritratto dell'iperaccudimento: un ventenne che non ha mai fatto nulla perché \"ci ha pensato la mamma\", e il rifiuto di invecchiare accudito da un robot che ti mette la flebo."),
            (190, 246, "Da cinico a romantico: la ribellione gentile",
             "cinico romantico cambiamento acidità vita non scherzetto ribellione soft gentile ironia teatri speranza pubblico",
             "Il cambiamento dichiarato di Crepet: dopo una vita da cinico, diventare romantico. La ribellione non è spallate ma una \"ribellione soft\", più gentile, che nel rapporto col pubblico usa l'ironia perché è più forte."),
            (370, 426, "La storia del fazzoletto: genitori pronti come pantere",
             "fazzoletto stazione madre pantera bambino raffreddore soffia iperaccudimento adulti adolescentizzazione",
             "La scena surreale del fazzoletto in stazione: una madre \"pronta come una pantera\" che interviene sul bambino. È l'istantanea di una genitorialità che sostituisce l'autonomia invece di insegnarla."),
            (730, 786, "ChatGPT come un prete di campagna",
             "ChatGPT ragazza sentimenti intelligenza artificiale prete di campagna anni 60 coccolare consenso clausole legali 24 ore",
             "La ragazza che di notte chiede a ChatGPT cosa fare con il fidanzato: l'algoritmo risponde come un prete di campagna degli anni Sessanta, perché è programmato per coccolarti e mai per dirti davvero la verità."),
            (1030, 1086, "Genitori giovanilisti e il regno del consenso",
             "genitori giovanilisti stessa musica dei figli consenso followers like adolescentizzazione adulti amicizia genitori figli",
             "L'adolescentizzazione degli adulti: genitori vestiti come i figli, che ascoltano la stessa musica e cercano consenso. L'amicizia tra famiglie non nasce più dai genitori ma dai figli."),
            (1270, 1326, "L'edicola Periodico 111: l'antitesi della comodità",
             "edicola Periodico 111 Firenze libreria Todo modo giornali poetessa Instagram giovani ribelli orario 5 del mattino",
             "Due ragazzi a Firenze hanno riaperto un'edicola chiusa da vent'anni che vende giornali alle cinque del mattino e ha anche una libreria: un'impresa eroica di ragazzi che fanno esattamente il contrario della comodità."),
            (1690, 1746, "Mike e la bambina: umanità contro robot",
             "Mike Harley Davidson volontario oncologia pediatrica bambina favole papà fino a quando muoio umanità robot assistenza Cina",
             "La storia di Mike, tatuato volontario in oncologia pediatrica, e della bambina che gli chiede di fare il papà \"fino a quando muoio\": l'umanità che nessun robot da assistenza potrà mai sostituire."),
            (1870, 1926, "Giocare vuol dire imparare a perdere",
             "giocare perdere nascondino PlayStation videogiochi anni 80 frustrazione riprovare nascondere rifiuto bambine figlie social",
             "Giocare è imparare a perdere: il nascondino in cui ti prendono sempre, i videogiochi anni Ottanta senza salvataggi. La frustrazione è pedagogica, e la generazione che non l'ha conosciuta teme il rifiuto."),
            (2290, 2346, "Le \"carte norvegesi\": tablet e sottotitoli persi",
             "Norvegia tablet scuola digitale coorte sottotitoli concentrazione lettura togliere i tablet gessetti infanzia esperimento",
             "L'esperimento norvegese: tablet a tutti i bambini, poi la coorte studiata a dieci anni di distanza non riesce più a leggere i sottotitoli dei film. Il governo toglie i tablet e riporta i gessetti, senza resistenze."),
            (2490, 2546, "Mario Lodi e l'Alfredo",
             "Mario Lodi Alfredo sacchetti colori non copio più lavagna Costituzione diritti bambini opportunità carta dipingere pedagogia",
             "Mario Lodi e il bambino che non vuole più copiare la lavagna ma lavorare con i colori dei sacchetti: l'idea che un bambino è un essere umano con talento e che il compito dell'adulto è dare possibilità, non modelli."),
            (2720, 2776, "L'eremita portato al liceo",
             "eremita montagna liceo La Spezia lavagna silenzio sguardo ragazzi memoria domande diversità obvietà",
             "Crepet porta in un liceo un eremita che vive in montagna da solo: sta zitto, non teme lo sguardo, e i ragazzi rimangono folgorati. Un insegnamento sulla diversità che diventa ovvietà e resta in memoria vent'anni dopo."),
            (3020, 3076, "La violenza come assenza di visione",
             "violenza giovani adolescenza precoce social pusher genitori coltellino seduzione della violenza frustrazione progetto visione operaio",
             "La violenza giovanile non nasce dai social ma viene da lì riprodotta: chi è violento non ha un'idea, non ha progetto. Più sei frustrato e senza visione, più la violenza diventa l'unico modo per esprimerti."),
            (3440, 3496, "Anna da Teheran: la valigia in venti minuti",
             "Anna 30 anni Teheran Iran valigia 20 minuti missili laurea Italia angeli ponte scrivere senso essere utili",
             "La mail di Anna, trent'anni, da Teheran: \"Lei lo sa cosa vuol dire farsi una valigia in venti minuti?\". È il segno che scrivere e parlare serve ancora, che esiste un ponte: il senso di essere arrivato a settantacinque anni."),
            (3680, 3736, "Basaglia al muro di Berlino",
             "Basaglia Berlino porta di Brandeburgo muro maggio 1980 manicomi buttar giù muri più alti invisibili paura profeta maggio tigli",
             "Alla porta di Brandeburgo, davanti al muro, Crepet rimprovera Basaglia di parlar male dei muri proprio lui che ha abbattuto quelli dei manicomi. La risposta: quando se ne butta giù uno, qualcuno ne costruisce uno più alto e invisibile."),
            (3910, 3966, "\"Vara che a viaggiare desincantar\"",
             "Venezia baretto barista muto angelo Matusalemme veneziano viaggiare disincanta Ungaretti voce scrivere malinconia uscire",
             "L'angelo che spinge Crepet a uscire di casa malgrado la malinconia: nel baretto, un signore antichissimo gli dice in veneziano \"vara che a viaggiare desincantar\". Da quell'incontro, non più previsto, ricomincia a scrivere."),
            (4160, 4216, "L'albergo Angst e il tubetto di dentifricio",
             "Bordighera grande albergo Angst paura vecchio albergo reali inglesi tubetto dentifricio vita spremere metafora stagioni",
             "L'anziano artista tedesco che racconta del \"grande albergo Angst\" di Bordighera: noi siamo un grande albergo che ha paura. E la vita come un tubetto di dentifricio, che dura sempre più di quanto si pensi."),
        ],
    },
    {
        "video_id": "MHxuu0-TGJk",
        "title": "Il Fienile Podcast - Episodio 9 - \"Il maestro di vita\" con Paolo Crepet",
        "channel": "il fienile",
        "clips": [
            (10, 66, "Peggy Guggenheim e il tè affumicato a Venezia",
             "Peggy Guggenheim Jackson Pollock Venezia tè affumicato collezionista arte contemporanea 19 anni incontro",
             "A diciannove anni Crepet beve un tè affumicato con Peggy Guggenheim davanti a un Pollock: l'arte contemporanea e le persone che l'hanno costruita, incontrate per caso a Venezia."),
            (250, 306, "Il nonno pittore e Modigliani",
             "nonno Angelo Maria pittore Accademia Venezia Modigliani compagno di corso carteggio collezionista scoperta morte del padre",
             "Il nonno veneziano Angelo Maria, pittore diplomatico accademico e compagno di corso di Modigliani: una storia di famiglia scoperta solo dopo la morte del padre, tramite il carteggio di un collezionista."),
            (380, 436, "Papà Massimo, Marghera e il primo studio sui tumori",
             "padre Massimo Crepet prorettore Padova medicina del lavoro Marghera ceramiche Bassano cancerogenesi ambientale primo centro anni 80",
             "Il padre Massimo, prorettore a Padova e medico del lavoro a Marghera: apre il primo centro di cancerogenesi ambientale in un'area dove il lavoro si barattava con la salute."),
            (520, 576, "Il colpo di testa alla Montessori",
             "Montessori credenza piatti spaccati maestre avariato non si riprende nonno ceramista forno vetro magia cottura",
             "Il bambino Crepet, innamorato della ceramica del nonno, alla Montessori tira giù una credenza di piatti: le maestre lo dichiarano \"avariato\" e lo rimandano a casa. Un carattere che rifiuta i modelli."),
            (740, 796, "Terzian, la severità e le competenze emisferiche",
             "Terzian neurochirurgo severità orologio esame università Padova competenze emisferiche cervello destra sinistra da Leore",
             "Il professor Terzian, maestro severissimo che con un gesto dell'orologio segnava la fine dell'esame, e che per primo descrisse le competenze emisferiche del cervello."),
            (850, 906, "La TAC pagata da John Lennon",
             "TAC John Lennon Beatles Abbey Road ingegnere prototipo da Leore neurochirurgia RX rivoluzionaria finanziatore rock",
             "Il professor da Leore rivela che il primo prototipo di TAC fu finanziato da John Lennon: i Beatles non solo facevano casino, ma hanno pagato un'invenzione che ha cambiato la medicina."),
            (980, 1036, "L'eroina a Verona negli anni Settanta",
             "eroina Verona anni 70 Piazza Dante studenti medicina professore bustine tabacco talco calce medicina legale ragazzi morti",
             "Gli anni Settanta a Verona: gli studenti di medicina mandati dal professore a comprare le bustine di eroina in Piazza Dante per analizzarle. Un'intera generazione che non ce l'ha fatta."),
            (1090, 1146, "\"In quale organo sta la malattia mentale?\"",
             "padre organo malattia mentale psichiatria sociologia Arezzo papà organico battuta non è lì innamorata",
             "La domanda del padre, medico organicista: \"La malattia mentale dov'è? In quale organo sta?\". Un dialogo che spiega la scelta di Crepet di andare verso i \"matti\" e la sociologia."),
            (1270, 1326, "L'odore dei manicomi",
             "Basaglia manicomi odore insopportabile dignità disuguaglianze olfattive 120000 internati diritti sposarsi votare questura",
             "Perché Basaglia aprì i manicomi: non poteva sopportare quell'odore. Crepet ne fa una riflessione sulla dignità e sulle disuguaglianze anche olfattive, in un'Italia con 120.000 internati senza diritti."),
            (1460, 1516, "La bistecca e il capo della psichiatria dell'OMS",
             "bistecca ospedale psichiatrico capo della psichiatria mondiale OMS Ginevra concorso India Praga Toronto Harvard",
             "Il giovane Crepet accetta una bistecca solo per un pasto migliore e scopre che il commensale era il capo della psichiatria mondiale: da lì il concorso all'OMS e i manicomi in India, a Praga e a Toronto."),
            (1580, 1636, "\"Tuo padre non ti ha mai detto bravo\"",
             "padre mai detto bravo avvocato funerale ciliegina sulla torta maestri torta devi farla tu pianista pianoforte",
             "Crepet scopre al funerale del padre, per bocca di un amico, che il padre lo considerava bravo. La lezione: i maestri ti possono lasciare la ciliegina, ma la torta la devi avere fatta tu — non si diventa pianisti comprando un pianoforte."),
            (1870, 1926, "L'India e la gioia di un figlio che compie un anno",
             "India Chandigarh Le Corbusier autista ambassador Indira Gandhi otto figli primo anno di vita rispetto per la vita povertà",
             "In India Crepet chiede all'autista quanti figli abbia: otto. \"Tu non puoi sapere la gioia quando tuo figlio compie il primo anno — normalmente non ci riesce.\" Un invito al rispetto per la vita."),
            (2000, 2056, "I libri con le sottolineature stampate",
             "Cecoslovacchia Breznev libri sottolineature stampate oppressione della mente Spartak Praga scarpe da tennis Berlino",
             "A Praga, sotto il muro, i libri di Breznev venivano stampati già sottolineati: l'oppressione della mente in Cecoslovacchia, e la dottoressa dello Spartak Praga disposta a rischiare la vita per un paio di scarpe da tennis."),
            (2140, 2196, "Jan Palach e Praga 1968",
             "Praga primavera 1968 carri armati sovietici Jan Palach 23 anni libertà giugno papà congresso cingoli ragazzi impossibile",
             "Crepet ragazzo a Praga nel giugno 1968, tra i ragazzi in piazza, e poi i carri armati sovietici a settembre: il sacrificio di Jan Palach, ventitreenne, per la libertà."),
            (2310, 2366, "Ramon de Pardon al manicomio: le carte, non la macchina",
             "Ramon de Pardon fotografo Magnum manicomio Trieste giocare a carte macchina fotografica Basaglia maggiolino livello",
             "Il fotografo Ramon de Pardon al manicomio di Trieste: invece di mostrare la macchina si mette a giocare a carte al tavolo. È il segreto del reportage: mettersi al livello delle persone, non osservarle dall'alto."),
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
        for idx, (st, en, topic, ents, summ) in enumerate(v["clips"], 1):
            assert 4 <= en - st <= 60, f"{vid} clip {idx} duration {en-st}s out of policy"
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
                "topics": [topic] + [t.strip() for t in ents.split()[:8]],
                "tags": [t for t in ents.split()[:12]],
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
                "folder_id": "1IrrIv7UF5ifgjL6ShBdO22N2P42gdhRP",
                "create_subfolder": False,
            },
            "segments": segments,
        }
        (OUT / f"payload_{vid}.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2))
        print(f"{vid}: {len(segments)} clips, payload written")
    (OUT / "crepet_clips.json").write_text(json.dumps(deliverable, ensure_ascii=False, indent=2))
    print(f"deliverable written -> {OUT / 'crepet_clips.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
