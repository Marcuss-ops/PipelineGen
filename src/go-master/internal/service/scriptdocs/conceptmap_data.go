package scriptdocs

// conceptMap maps Italian concepts across 7 languages to Artlist search terms.
// Covers ALL 35 terms in the Artlist SQLite DB.
// Each entry maps multilingual keywords to the EXACT Artlist term name.
var conceptMap = []clipConcept{
	// === PEOPLE (100 clips) ===
	{[]string{
		// Italian
		"persone", "persona", "uomo", "donna", "gente", "pubblico", "follower", "giovani", "fan", "influenza", "influenzare", "popolo", "folla", "multitudine", "comunità", "individuo", "umanit",
		// English
		"people", "person", "crowd", "audience", "followers", "fans", "public", "mob", "community", "individual", "humanity",
		// French
		"personnes", "gens", "foule", "public", "fans", "influence", "communauté", "individu",
		// Spanish
		"personas", "gente", "público", "seguidores", "fans", "influencia", "comunidad", "individuo",
		// German
		"menschen", "publikum", "fans", "anhänger", "einfluss", "gemeinschaft", "individuum",
		// Portuguese
		"pessoas", "público", "seguidores", "fãs", "influência", "comunidade", "indivíduo",
		// Romanian
		"oameni", "public", "fani", "influență", "comunitate", "individ",
	}, "people", 0.50},

	// === CITY (50 clips) ===
	{[]string{
		// Italian
		"città", "citta", "Romania", "Washington", "arresto", "polizia", "carcere", "crimine", "accuse", "violenza", "criminale", "tribunale", "prigione", "metropoli", "centro storico",
		// English
		"city", "arrest", "police", "prison", "crime", "violence", "criminal", "accusation", "court", "jail", "metropolis",
		// French
		"ville", "arrestation", "police", "prison", "crime", "violence", "criminel", "tribunal", "métropole",
		// Spanish
		"ciudad", "arresto", "policía", "prisión", "crimen", "violencia", "criminal", "tribunal", "metrópoli",
		// German
		"stadt", "verhaftung", "polizei", "gefängnis", "verbrechen", "gewalt", "gericht", "metropole",
		// Portuguese
		"cidade", "prisão", "polícia", "crime", "violência", "criminal", "tribunal", "metrópole",
		// Romanian
		"oraș", "arestat", "poliție", "închisoare", "crimă", "violență", "tribunal", "metropolă",
	}, "city", 0.90},

	// === TECHNOLOGY (50 clips) ===
	{[]string{
		// Italian
		"tech", "tecnologia", "online", "internet", "digitale", "piattaforma", "social", "tiktok", "youtube", "smartphone", "telefono", "computer", "software", "algoritmo", "app", "sito",
		// English
		"technology", "tech", "online", "internet", "digital", "platform", "social media", "smartphone", "youtube", "tiktok", "computer", "software", "algorithm", "app", "website",
		// French
		"technologie", "en ligne", "internet", "numérique", "plateforme", "réseaux sociaux", "smartphone", "ordinateur",
		// Spanish
		"tecnología", "en línea", "internet", "digital", "plataforma", "redes sociales", "teléfono", "computadora",
		// German
		"technologie", "online", "internet", "digital", "plattform", "soziale medien", "smartphone", "computer",
		// Portuguese
		"tecnologia", "online", "internet", "digital", "plataforma", "redes sociais", "computador",
		// Romanian
		"tehnologie", "online", "internet", "digital", "platformă", "rețele sociale", "calculator",
	}, "technology", 0.80},

	// === BUSINESS (100 clips) ===
	{[]string{
		// Italian
		"soldi", "finanza", "business", "azienda", "economia", "investimento", "denaro", "banca", "startup", "ufficio", "riunione", "lavoro", "carriera", "successo", "profitto", "manager", "imprenditore", "negoziare", "commercio",
		// English
		"money", "finance", "business", "company", "economy", "investment", "cash", "bank", "startup", "office", "meeting", "work", "career", "success", "profit", "manager", "entrepreneur", "trade",
		// French
		"argent", "finance", "affaires", "entreprise", "économie", "investissement", "bureau", "carrière", "succès", "profit", "manager",
		// Spanish
		"dinero", "finanzas", "negocios", "empresa", "economía", "inversión", "oficina", "carrera", "éxito", "ganancia", "gerente",
		// German
		"geld", "finanzen", "geschäft", "firma", "wirtschaft", "investition", "büro", "karriere", "erfolg", "gewinn", "manager",
		// Portuguese
		"dinheiro", "finanças", "negócios", "empresa", "economia", "investimento", "escritório", "carreira", "sucesso", "lucro", "gerente",
		// Romanian
		"bani", "finanțe", "afaceri", "companie", "economie", "investiție", "birou", "carieră", "succes", "profit", "manager",
	}, "business", 0.75},

	// === GYM (100 clips) ===
	{[]string{
		// Italian
		"gym", "palestra", "allenamento", "forza", "potenza", "fitness", "esercizio", "muscoli", "pesi", "bodybuilding", "crossfit", "atleta", "preparazione fisica", "workout",
		// English
		"gym", "workout", "training", "strength", "power", "fitness", "exercise", "muscle", "weights", "bodybuilding", "crossfit", "athlete",
		// French
		"gym", "entraînement", "force", "puissance", "fitness", "exercice", "muscle", "haltères", "athlète",
		// Spanish
		"gimnasio", "entrenamiento", "fuerza", "poder", "fitness", "ejercicio", "músculo", "pesas", "atleta",
		// German
		"fitnessstudio", "training", "stärke", "kraft", "fitness", "übung", "muskel", "gewichte", "athlet",
		// Portuguese
		"academia", "treino", "força", "poder", "fitness", "exercício", "músculo", "pesos", "atleta",
		// Romanian
		"sală", "antrenament", "putere", "fitness", "exercițiu", "mușchi", "greutăți", "atlet",
	}, "gym", 0.70},

	// === FLOYD MAYWEATHER (specific person) ===
	{[]string{
		"floyd", "mayweather", "mayweather jr", "money", "pretty boy",
	}, "floyd", 0.95},

	// === MIKE TYSON (specific person) ===
	{[]string{
		"tyson", "iron mike", "mike tyson", "baddest man on the planet",
	}, "tyson", 0.95},

	// === BOXING / COMBAT (specific sport) ===
	{[]string{
		// Italian
		"combattimento", "lotta", "boxe", "pugile", "pugilato", "knockout", "ko", "campione", "medaglia", "torneo", "ring", "guantoni", "combattente",
		// English
		"boxing", "boxer", "knockout", "champion", "tournament", "ring", "gloves", "fighter", "combat", "punch", "uppercut", "jab", "hook", "referee",
		// French
		"combat", "boxe", "boxeur", "force", "puissance", "champion", "médaille", "ring", "combattant",
		// Spanish
		"pelea", "boxeo", "boxeador", "fuerza", "poder", "campeón", "medalla", "ring", "combatiente",
		// German
		"kampf", "boxen", "boxer", "stärke", "kraft", "meister", "medaille", "kämpfer",
		// Portuguese
		"luta", "boxe", "boxeador", "esporte", "força", "poder", "campeão", "medalha", "lutador",
		// Romanian
		"luptă", "box", "boxer", "sport", "putere", "campion", "medalie", "luptător",
	}, "gym", 0.78},

	// === RUNNING (100 clips) ===
	{[]string{
		// Italian
		"correre", "corsa", "maratona", "sprint", "jogging", "atletica", "velocità", "resistenza", "running", "trail",
		// English
		"running", "jogging", "marathon", "sprint", "track", "athletics", "speed", "endurance", "trail", "runner",
		// French
		"course", "jogging", "marathon", "sprint", "athlétisme", "vitesse", "endurance", "coureur",
		// Spanish
		"correr", "carrera", "maratón", "sprint", "atletismo", "velocidad", "resistencia", "corredor",
		// German
		"laufen", "joggen", "marathon", "sprint", "leichtathletik", "geschwindigkeit", "ausdauer", "läufer",
		// Portuguese
		"correr", "corrida", "maratona", "sprint", "atletismo", "velocidade", "resistência", "corredor",
		// Romanian
		"alearga", "maraton", "sprint", "atletism", "viteză", "rezistență", "alergător",
	}, "running", 0.75},

	// === YOGA (100 clips) ===
	{[]string{
		// Italian
		"yoga", "meditazione", "stretching", "flessibilità", "respiro", "mindfulness", "zen", "posizione", "asana", "relax", "benessere",
		// English
		"yoga", "meditation", "stretching", "flexibility", "breathing", "mindfulness", "zen", "pose", "asana", "relaxation", "wellness",
		// French
		"yoga", "méditation", "étirement", "flexibilité", "respiration", "pleine conscience", "zen", "posture", "relaxation",
		// Spanish
		"yoga", "meditación", "estiramiento", "flexibilidad", "respiración", "atención plena", "zen", "postura", "relajación",
		// German
		"yoga", "meditation", "dehnen", "flexibilität", "atmung", "achtsamkeit", "zen", "haltung", "entspannung",
		// Portuguese
		"yoga", "meditação", "alongamento", "flexibilidade", "respiração", "atenção plena", "zen", "postura", "relaxamento",
		// Romanian
		"yoga", "meditație", "întindere", "flexibilitate", "respirație", "zen", "poziție", "relaxare",
	}, "yoga", 0.78},

	// === SOCCER (100 clips) ===
	{[]string{
		// Italian
		"calcio", "soccer", "goal", "gol", "tifosi", "pallone", "campo da calcio", "football",
		// English
		"soccer", "football", "goalkeeper", "pitch", "penalty", "striker", "referee", "jersey", "league",
		// French
		"football", "soccer", "gardien", "stade", "équipe", "joueur", "ballon",
		// Spanish
		"fútbol", "gol", "estadio", "aficionados", "equipo", "jugador", "pelota",
		// German
		"fussball", "tor", "stadion", "mannschaft", "spieler", "ball",
		// Portuguese
		"futebol", "gol", "estádio", "equipe", "jogador", "bola",
		// Romanian
		"fotbal", "gol", "stadion", "echipă", "jucător", "minge",
	}, "soccer", 0.40},

	// === SWIMMING (100 clips) ===
	{[]string{
		// Italian
		"nuoto", "swimming", "piscina", "mare", "acqua", "vasca", "stile libero", "tuffo", "subacqueo", "ond",
		// English
		"swimming", "pool", "sea", "water", "dive", "underwater", "wave", "splash", "stroke",
		// French
		"natation", "piscine", "mer", "eau", "plongeon", "sous-marin", "vague",
		// Spanish
		"natación", "piscina", "mar", "agua", "buceo", "submarino", "ola",
		// German
		"schwimmen", "pool", "meer", "wasser", "tauchen", "unterwasser", "welle",
		// Portuguese
		"natação", "piscina", "mar", "água", "mergulho", "subaquático", "onda",
		// Romanian
		"înot", "piscină", "mare", "apă", "scufundare", "val",
	}, "swimming", 0.75},

	// === NATURE (50 clips) ===
	{[]string{
		// Italian
		"natura", "naturale", "ambiente", "ecosistema", "verde", "paesaggio naturale", "flora", "fauna", "biodiversità", "campagna",
		// English
		"nature", "natural", "environment", "ecosystem", "green", "landscape", "flora", "fauna", "biodiversity", "countryside",
		// French
		"nature", "naturel", "environnement", "écosystème", "vert", "paysage", "flore", "faune",
		// Spanish
		"naturaleza", "natural", "ambiente", "ecosistema", "verde", "paisaje", "flora", "fauna",
		// German
		"natur", "natürlich", "umwelt", "ökosystem", "grün", "landschaft", "flora", "fauna",
		// Portuguese
		"natureza", "natural", "ambiente", "ecossistema", "verde", "paisagem", "flora", "fauna",
		// Romanian
		"natură", "natural", "mediu", "ecosistem", "verde", "peisaj", "floră", "faună",
	}, "nature", 0.82},

	// === SUNSET (100 clips) ===
	{[]string{
		// Italian
		"tramonto", "sunset", "sera", "crepuscolo", "aurora", "alba", "orizzonte", "cielo rosso", "golden hour", "luce dorata",
		// English
		"sunset", "evening", "dusk", "dawn", "sunrise", "horizon", "golden hour", "twilight", "sky", "orange sky",
		// French
		"coucher de soleil", "soir", "crépuscule", "aube", "horizon", "heure dorée", "ciel orange",
		// Spanish
		"atardecer", "puesta de sol", "noche", "crepúsculo", "amanecer", "horizonte", "hora dorada",
		// German
		"sonnenuntergang", "abend", "dämmerung", "morgenröte", "horizont", "goldene stunde", "himmel",
		// Portuguese
		"pôr do sol", "anoitecer", "crepúsculo", "amanhecer", "horizonte", "hora dourada", "céu",
		// Romanian
		"apus", "seară", "amurg", "zori", "orizont", "cer portocaliu",
	}, "sunset", 0.85},

	// === OCEAN (100 clips) ===
	{[]string{
		// Italian
		"oceano", "ocean", "mare aperto", "onde", "marea", "costa", "spiaggia oceanica", "acqua salata", "profondità marine", "tempesta marina",
		// English
		"ocean", "sea", "waves", "tide", "coast", "deep sea", "saltwater", "storm", "beach", "horizon",
		// French
		"océan", "mer", "vagues", "marée", "côte", "profondeur marine", "tempête", "plage",
		// Spanish
		"océano", "mar", "olas", "marea", "costa", "profundidad", "tormenta", "playa",
		// German
		"ozean", "meer", "wellen", "gezeiten", "küste", "tiefer see", "sturm", "strand",
		// Portuguese
		"oceano", "mar", "ondas", "maré", "costa", "profundidade", "tempestade", "praia",
		// Romanian
		"ocean", "mare", "valuri", "maree", "coastă", "adâncime", "furtună", "plajă",
	}, "ocean", 0.83},

	// === MOUNTAIN (100 clips) ===
	{[]string{
		// Italian
		"montagna", "mountain", "vetta", "cima", "alpinismo", "escalation", "neve", "ghiacciaio", "valle", "sentiero di montagna", "picco",
		// English
		"mountain", "peak", "summit", "climbing", "snow", "glacier", "valley", "trail", "ridge", "alpine",
		// French
		"montagne", "sommet", "pic", "escalade", "neige", "glacier", "vallée", "sentier", "alpin",
		// Spanish
		"montaña", "cima", "cumbre", "escalada", "nieve", "glaciar", "valle", "sendero", "alpino",
		// German
		"berg", "gipfel", "spitze", "klettern", "schnee", "gletscher", "tal", "pfad", "alpin",
		// Portuguese
		"montanha", "pico", "cume", "escalada", "neve", "geleira", "vale", "trilha", "alpino",
		// Romanian
		"munte", "vârf", "culme", "escaladă", "zăpadă", "ghețar", "vale", "potecă", "alpin",
	}, "mountain", 0.80},

	// === FOREST (100 clips) ===
	{[]string{
		// Italian
		"foresta", "forest", "bosco", "alberi", "legno", "vegetazione", "canopia", "sottobosco", "radura", "pineta", "giungla",
		// English
		"forest", "woods", "trees", "timber", "vegetation", "canopy", "undergrowth", "clearing", "pine", "jungle", "woodland",
		// French
		"forêt", "bois", "arbres", "végétation", "canopée", "sous-bois", "clairière", "pin", "jungle",
		// Spanish
		"bosque", "selva", "árboles", "vegetación", "dosel", "sotobosque", "claro", "pino", "jungla",
		// German
		"wald", "bäume", "vegetation", "kronendach", "unterholz", "lichtung", "kiefer", "dschungel",
		// Portuguese
		"floresta", "mata", "árvores", "vegetação", "dossel", "clareira", "pinheiro", "selva",
		// Romanian
		"pădure", "copaci", "vegetație", "coroană", "poiană", "pin", "junglă",
	}, "forest", 0.80},

	// === RAIN (100 clips) ===
	{[]string{
		// Italian
		"pioggia", "rain", "temporale", "acquazzone", "gocce", "ombrello", "bagnato", "nubi", "temporale", "clima umido", "rovescio",
		// English
		"rain", "raindrop", "shower", "storm", "downpour", "umbrella", "wet", "clouds", "humid", "thunderstorm",
		// French
		"pluie", "averse", "orage", "gouttes", "parapluie", "mouillé", "nuages", "humide",
		// Spanish
		"lluvia", "tormenta", "aguacero", "gotas", "paraguas", "mojado", "nubes", "húmedo",
		// German
		"regen", "sturm", "schauer", "tropfen", "regenschirm", "nass", "wolken", "feucht",
		// Portuguese
		"chuva", "tempestade", "gota", "guarda-chuva", "molhado", "nuvens", "úmido",
		// Romanian
		"ploaie", "furtună", "picături", "umbrelă", "ud", "norii", "umed",
	}, "rain", 0.78},

	// === SNOW (100 clips) ===
	{[]string{
		// Italian
		"neve", "snow", "fiocco di neve", "inverno", "freddo", "bianco", "gelo", "nevicata", "manto nevoso", "valanga",
		// English
		"snow", "snowflake", "winter", "cold", "white", "frost", "snowfall", "blizzard", "avalanche", "ice",
		// French
		"neige", "flocon", "hiver", "froid", "blanc", "gel", "chute de neige", "blizzard", "avalanche",
		// Spanish
		"nieve", "copo", "invierno", "frío", "blanco", "helada", "nevada", "ventisca", "avalancha",
		// German
		"schnee", "schneeflocke", "winter", "kalt", "weiß", "frost", "schneefall", "lawine",
		// Portuguese
		"neve", "floco de neve", "inverno", "frio", "branco", "geada", "nevasca", "avalanche",
		// Romanian
		"zăpadă", "fulg", "iarnă", "rece", "alb", "îngheț", "viscol", "avalanșă",
	}, "snow", 0.78},

	// === COOKING (100 clips) ===
	{[]string{
		// Italian
		"cucina", "cooking", "cucinare", "chef", "cibo", "ricetta", "padella", "forno", "tagliare", "preparazione", "piatto", "pasto", "ingredienti", "ristorante",
		// English
		"cooking", "kitchen", "chef", "food", "recipe", "pan", "oven", "cut", "preparation", "dish", "meal", "ingredients", "restaurant",
		// French
		"cuisine", "cuisiner", "chef", "nourriture", "recette", "poêle", "four", "plat", "repas", "ingrédients", "restaurant",
		// Spanish
		"cocina", "cocinar", "chef", "comida", "receta", "sartén", "horno", "plato", "comida", "ingredientes", "restaurante",
		// German
		"küche", "kochen", "koch", "essen", "rezept", "pfanne", "ofen", "gericht", "mahlzeit", "zutaten", "restaurant",
		// Portuguese
		"cozinha", "cozinhar", "chef", "comida", "receita", "panela", "forno", "prato", "refeição", "ingredientes", "restaurante",
		// Romanian
		"bucătărie", "gătit", "bucătar", "mâncare", "rețetă", "tigaie", "cuptor", "farfurie", "masă", "ingrediente", "restaurant",
	}, "cooking", 0.82},

	// === DOG (100 clips) ===
	{[]string{
		// Italian
		"cane", "dog", "cucciolo", "peloso", "pastore tedesco", "labrador", "golden retriever", "bulldog", "randagio", "addestramento cani", "guau",
		// English
		"dog", "puppy", "canine", "pet", "labrador", "golden retriever", "bulldog", "stray dog", "dog training", "bark", "paw",
		// French
		"chien", "chiot", "canin", "animal", "labrador", "retriever", "bouledogue", "chien errant", "aboiement",
		// Spanish
		"perro", "cachorro", "canino", "mascota", "labrador", "bulldog", "perro callejero", "ladrido",
		// German
		"hund", "welpe", "canin", "haustier", "labrador", "bulldogge", "streuner", "bellen", "pfote",
		// Portuguese
		"cachorro", "cão", "filhote", "canino", "animal de estimação", "labrador", "bulldog", "latido",
		// Romanian
		"câine", "cățel", "canin", "animal de companie", "labrador", "bulldog", "lătrat", "labă",
	}, "dog", 0.85},

	// === CAT (100 clips) ===
	{[]string{
		// Italian
		"gatto", "cat", "micio", "felino", "gattino", "randagio", "gatto domestico", "fusa", "miagolare", "zampa",
		// English
		"cat", "kitten", "feline", "pet", "stray cat", "domestic cat", "purr", "meow", "paw", "whisker",
		// French
		"chat", "chaton", "félin", "animal", "chat domestique", "ronronnement", "miaou", "patte",
		// Spanish
		"gato", "gatito", "felino", "mascota", "gato callejero", "ronroneo", "maullido", "pata",
		// German
		"katze", "kätzchen", "feline", "haustier", "streuner", "schnurren", "miauen", "pfote",
		// Portuguese
		"gato", "gatinho", "felino", "animal de estimação", "gato de rua", "ronronar", "miar", "pata",
		// Romanian
		"pisică", "pisoi", "felina", "animal de companie", "pisică străină", "tors", "meau", "labă",
	}, "cat", 0.85},

	// === BIRD (100 clips) ===
	{[]string{
		// Italian
		"uccello", "bird", "volatile", "piume", "volo", "nido", "canto degli uccelli", "ala", "becco", "aquila", "colomba", "passero",
		// English
		"bird", "flying", "feather", "nest", "wing", "beak", "song", "eagle", "dove", "sparrow", "avian",
		// French
		"oiseau", "vol", "plume", "nid", "aile", "bec", "chant", "aigle", "colombe", "moineau",
		// Spanish
		"pájaro", "ave", "pluma", "nido", "vuelo", "ala", "pico", "canto", "águila", "paloma", "gorrión",
		// German
		"vogel", "flug", "feder", "nest", "flügel", "schnabel", "gesang", "adler", "taube", "sperling",
		// Portuguese
		"pássaro", "ave", "pena", "ninho", "voo", "asa", "bico", "canto", "águia", "pomba", "pardal",
		// Romanian
		"pasăre", "zbor", "pană", "cuib", "aripă", "cioc", "cântec", "vultur", "porumbel", "vrabie",
	}, "bird", 0.82},

	// === HORSE (100 clips) ===
	{[]string{
		// Italian
		"cavallo", "horse", "cavalcare", "stalla", "galoppo", "cucciolo di cavallo", "equitazione", "ferro di cavallo", "criniera", "mandria",
		// English
		"horse", "riding", "stable", "gallop", "foal", "equestrian", "horseshoe", "mane", "herd", "saddle",
		// French
		"cheval", "équitation", "écurie", "galop", "poulain", "fer à cheval", "crinière", "troupeau",
		// Spanish
		"caballo", "equitación", "establo", "galope", "potro", "herradura", "crin", "rebaño",
		// German
		"pferd", "reiten", "stall", "galopp", "fohlen", "hufeisen", "mähne", "herde",
		// Portuguese
		"cavalo", "cavalgar", "estábulos", "galope", "potro", "ferradura", "crina", "rebanho",
		// Romanian
		"cal", "călărie", "grajd", "galop", "mînz", "potcoavă", "coamă", "turma",
	}, "horse", 0.82},

	// === BUTTERFLY (100 clips) ===
	{[]string{
		// Italian
		"farfalla", "butterfly", "bruco", "metamorfosi", "crisalide", "ala colorata", "bozzolo", "impollinazione", "natura piccola",
		// English
		"butterfly", "caterpillar", "metamorphosis", "chrysalis", "wings", "cocoon", "pollination", "insect", "colorful",
		// French
		"papillon", "chenille", "métamorphose", "chrysalide", "ailes", "cocon", "pollinisation", "insecte",
		// Spanish
		"mariposa", "oruga", "metamorfosis", "crisálida", "alas", "capullo", "polinización", "insecto",
		// German
		"schmetterling", "raupe", "metamorphose", "kokon", "flügel", "bestäubung", "insekt", "bunt",
		// Portuguese
		"borboleta", "lagarta", "metamorfose", "crisálida", "asas", "casulo", "polinização", "inseto",
		// Romanian
		"fluture", "omida", "metamorfoză", "crisalidă", "aripi", "cocon", "polenizare", "insectă",
	}, "butterfly", 0.80},

	// === SPIDER (100 clips) ===
	{[]string{
		// Italian
		"ragno", "spider", "ragnatela", "tessitore", "filo di seta", "vedova nera", "tarantola", "veleno", "zampa", " aracnide",
		// English
		"spider", "web", "web spinning", "silk", "tarantula", "venom", "arachnid", "insect", "leg", "black widow",
		// French
		"araignée", "toile", "soie", "tisser", "tarentule", "venin", "arachnide", "insecte",
		// Spanish
		"araña", "telaraña", "seda", "tejer", "tarántula", "veneno", "arácnido", "insecto",
		// German
		"spinne", "netz", "seide", "weben", "tarantel", "gift", "spinnentier", "insekt",
		// Portuguese
		"aranha", "teia", "seda", "tecer", "tarântula", "veneno", "aracnídeo", "inseto",
		// Romanian
		"păianjen", "pânză", "mătase", "țesut", "tarantulă", "venin", "arahinid", "insectă",
	}, "spider", 0.80},

	// === TRAVEL (100 clips) ===
	{[]string{
		// Italian
		"viaggio", "travel", "turismo", "vacanza", "esplorare", "avventura", "destinazione", "valigia", "passaporto", "aeroporto", "mappa", "itinerario",
		// English
		"travel", "tourism", "vacation", "holiday", "explore", "adventure", "destination", "suitcase", "passport", "airport", "map", "itinerary",
		// French
		"voyage", "tourisme", "vacances", "explorer", "aventure", "destination", "valise", "passeport", "aéroport",
		// Spanish
		"viaje", "turismo", "vacaciones", "explorar", "aventura", "destino", "maleta", "pasaporte", "aeropuerto",
		// German
		"reise", "tourismus", "urlaub", "erkunden", "abenteuer", "ziel", "koffer", "pass", "flughafen",
		// Portuguese
		"viagem", "turismo", "férias", "explorar", "aventura", "destino", "mala", "passaporte", "aeroporto",
		// Romanian
		"călătorie", "turism", "vacanță", "explora", "aventură", "destinație", "valiză", "pașaport", "aeroport",
	}, "travel", 0.78},

	// === CAR (100 clips) ===
	{[]string{
		// Italian
		"auto", "car", "macchina", "veicolo", "strada", "guida", "corsia", "traffico", "corsa", "velocità", "motore", "garage", "parcheggio",
		// English
		"car", "vehicle", "road", "driving", "lane", "traffic", "speed", "engine", "garage", "parking", "automobile",
		// French
		"voiture", "véhicule", "route", "conduite", "trafic", "vitesse", "moteur", "garage", "parking",
		// Spanish
		"coche", "auto", "vehículo", "carretera", "conducir", "tráfico", "velocidad", "motor", "garaje",
		// German
		"auto", "fahrzeug", "straße", "fahren", "verkehr", "geschwindigkeit", "motor", "garage", "parkplatz",
		// Portuguese
		"carro", "veículo", "estrada", "dirigir", "tráfego", "velocidade", "motor", "garagem", "estacionamento",
		// Romanian
		"mașina", "vehicul", "drum", "conducere", "trafic", "viteză", "motor", "garaj", "parcare",
	}, "car", 0.78},

	// === TRAIN (100 clips) ===
	{[]string{
		// Italian
		"treno", "train", "ferrovia", "binario", "stazione", "locomotiva", "vagone", "binari", "pendolare", "viaggio in treno",
		// English
		"train", "railway", "track", "station", "locomotive", "carriage", "commuter", "rail", "journey",
		// French
		"train", "chemin de fer", "voie", "gare", "locomotive", "wagon", "navette", "rail",
		// Spanish
		"tren", "ferrocarril", "vía", "estación", "locomotora", "vagón", "cercanías", "rail",
		// German
		"zug", "eisenbahn", "gleis", "bahnhof", "lokomotive", "wagen", "pendler", "schienen",
		// Portuguese
		"trem", "comboio", "ferrovia", "via", "estação", "locomotiva", "vagão", "commutador",
		// Romanian
		"tren", "cale ferată", "șina", "gară", "locomotivă", "vagon", "navetist", "cale",
	}, "train", 0.78},

	// === AIRPLANE (100 clips) ===
	{[]string{
		// Italian
		"aereo", "airplane", "aeroplano", "volo", "aeroporto", "pista", "decollare", "atterraggio", "cielo", "ali", "jet", "elicottero", "aviazione",
		// English
		"airplane", "flight", "airport", "runway", "takeoff", "landing", "sky", "wing", "jet", "helicopter", "aviation", "aircraft",
		// French
		"avion", "vol", "aéroport", "piste", "décollage", "atterrissage", "ciel", "aile", "jet", "hélicoptère", "aviation",
		// Spanish
		"avión", "vuelo", "aeropuerto", "pista", "despegue", "aterrizaje", "cielo", "ala", "jet", "helicóptero", "aviación",
		// German
		"flugzeug", "flug", "flughafen", "startbahn", "start", "landung", "himmel", "flügel", "jet", "hubschrauber", "luftfahrt",
		// Portuguese
		"avião", "voo", "aeroporto", "pista", "decolagem", "pouso", "céu", "asa", "jato", "helicóptero", "aviação",
		// Romanian
		"avion", "zbor", "aeroport", "pistă", "decolare", "aterizare", "cer", "aripă", "jet", "elicopter", "aviație",
	}, "airplane", 0.78},

	// === CONCERT (100 clips) ===
	{[]string{
		// Italian
		"concerto", "concert", "palcoscenico", "stage", "live", "musica live", "fan", "spettacolo", "festival", "chitarra", "microfono", "rock", "pop", "DJ",
		// English
		"concert", "stage", "live music", "performance", "show", "festival", "guitar", "microphone", "rock", "pop", "DJ", "crowd", "audience",
		// French
		"concert", "scène", "musique live", "spectacle", "festival", "guitare", "microphone", "rock", "foule",
		// Spanish
		"concierto", "escenario", "música en vivo", "espectáculo", "festival", "guitarra", "micrófono", "rock", "multitud",
		// German
		"konzert", "bühne", "livemusik", "show", "festival", "gitarre", "mikrofon", "rock", "publikum",
		// Portuguese
		"show", "concerto", "palco", "música ao vivo", "espetáculo", "festival", "guitarra", "microfone", "rock", "multidão",
		// Romanian
		"concert", "scenă", "muzică live", "spectacol", "festival", "chitară", "microfon", "rock", "mulțime",
	}, "concert", 0.80},

	// === MUSIC (100 clips) ===
	{[]string{
		// Italian
		"musica", "music", "suonare", "strumento", "melodia", "ritmo", "canzone", "cantante", "note", "spartito", "piano", "batteria", "violino",
		// English
		"music", "play", "instrument", "melody", "rhythm", "song", "singer", "notes", "sheet music", "piano", "drums", "violin", "sound",
		// French
		"musique", "jouer", "instrument", "mélodie", "rythme", "chanson", "chanteur", "notes", "piano", "batterie", "violon",
		// Spanish
		"música", "tocar", "instrumento", "melodía", "ritmo", "canción", "cantante", "notas", "piano", "batería", "violín",
		// German
		"musik", "spielen", "instrument", "melodie", "rhythmus", "lied", "sänger", "noten", "klavier", "schlagzeug", "geige",
		// Portuguese
		"música", "tocar", "instrumento", "melodia", "ritmo", "canção", "cantor", "notas", "piano", "bateria", "violino",
		// Romanian
		"muzică", "cânta", "instrument", "melodie", "ritm", "cântec", "cântăreț", "note", "pian", "tobe", "vioară",
	}, "music", 0.80},

	// === DANCE (100 clips) ===
	{[]string{
		// Italian
		"danza", "dance", "ballare", "ballo", "movimento", "coreografia", "passo di danza", "hip hop", "balletto", "salsa", "contemporaneo",
		// English
		"dance", "dancing", "ballet", "choreography", "movement", "step", "hip hop", "salsa", "contemporary", "performer",
		// French
		"danse", "danser", "ballet", "chorégraphie", "mouvement", "pas", "hip hop", "salsa", "contemporain",
		// Spanish
		"danza", "baile", "ballet", "coreografía", "movimiento", "paso", "hip hop", "salsa", "contemporáneo",
		// German
		"tanz", "tanzen", "ballett", "choreografie", "bewegung", "schritt", "hip hop", "salsa", "zeitgenössisch",
		// Portuguese
		"dança", "dançar", "ballet", "coreografia", "movimento", "passo", "hip hop", "salsa", "contemporâneo",
		// Romanian
		"dans", "a dansa", "ballet", "coregrafie", "mișcare", "pas", "hip hop", "salsa", "contemporan",
	}, "dance", 0.78},

	// === PARTY (100 clips) ===
	{[]string{
		// Italian
		"festa", "party", "celebrazione", "divertimento", "ballo", "nightclub", "discoteca", "confetti", "brindisi", "festa di compleanno", "serata",
		// English
		"party", "celebration", "fun", "dancing", "nightclub", "confetti", "toast", "birthday", "nightlife", "gathering",
		// French
		"fête", "soirée", "célébration", "danse", "boîte de nuit", "confettis", "toast", "anniversaire",
		// Spanish
		"fiesta", "celebración", "diversión", "baile", "discoteca", "confeti", "brindis", "cumpleaños", "vida nocturna",
		// German
		"party", "feier", "spaß", "tanzen", "nachtclub", "konfetti", "toast", "geburtstag", "nachtleben",
		// Portuguese
		"festa", "celebração", "diversão", "dança", "boate", "confete", "brinde", "aniversário", "vida noturna",
		// Romanian
		"petrecere", "celebrare", "distracție", "dans", "club de noapte", "confetti", "toast", "aniversare",
	}, "party", 0.75},

	// === WEDDING (100 clips) ===
	{[]string{
		// Italian
		"matrimonio", "wedding", "sposa", "sposo", "nozze", "abito da sposa", "anello", "fedi", "cerimonia", "ricevimento", "bacio nuziale", "torta nuziale",
		// English
		"wedding", "bride", "groom", "marriage", "wedding dress", "ring", "ceremony", "reception", "kiss", "wedding cake", "vows",
		// French
		"mariage", "mariée", "marié", "robe de mariée", "anneau", "cérémonie", "réception", "baiser", "gâteau",
		// Spanish
		"boda", "novia", "novio", "matrimonio", "vestido de novia", "anillo", "ceremonia", "recepción", "beso", "pastel",
		// German
		"hochzeit", "braut", "bräutigam", "hochzeitskleid", "ring", "zeremonie", "empfang", "kuss", "torte",
		// Portuguese
		"casamento", "noiva", "noivo", "vestido de noiva", "anel", "cerimônia", "recepção", "beijo", "bolo",
		// Romanian
		"nuntă", "mireasă", "mire", "rochie de mireasă", "inel", "ceremonie", "recepție", "sărut", "tort",
	}, "wedding", 0.82},

	// === FAMILY (100 clips) ===
	{[]string{
		// Italian
		"famiglia", "family", "genitori", "bambini", "figli", "nonni", "bambino", "genitore", "unità familiare", "amore familiare", "generazioni",
		// English
		"family", "parents", "children", "kids", "grandparents", "child", "parent", "love", "generations", "together", "home",
		// French
		"famille", "parents", "enfants", "grands-parents", "enfant", "amour", "générations", "ensemble", "maison",
		// Spanish
		"familia", "padres", "niños", "hijos", "abuelos", "niño", "amor", "generaciones", "juntos", "hogar",
		// German
		"familie", "eltern", "kinder", "großeltern", "kind", "liebe", "generationen", "zusammen", "zuhause",
		// Portuguese
		"família", "pais", "crianças", "filhos", "avós", "criança", "amor", "gerações", "juntos", "lar",
		// Romanian
		"familie", "părinți", "copii", "bunici", "copil", "iubire", "generații", "împreună", "acasă",
	}, "family", 0.80},

	// === EDUCATION (100 clips) ===
	{[]string{
		// Italian
		"educazione", "education", "scuola", "studente", "insegnante", "classe", "lezione", "studiare", "libro", "università", "apprendimento", "conoscenza",
		// English
		"education", "school", "student", "teacher", "classroom", "lesson", "study", "book", "university", "learning", "knowledge", "lecture",
		// French
		"éducation", "école", "étudiant", "professeur", "classe", "leçon", "étudier", "livre", "université", "apprentissage",
		// Spanish
		"educación", "escuela", "estudiante", "profesor", "clase", "lección", "estudiar", "libro", "universidad", "aprendizaje",
		// German
		"bildung", "schule", "schüler", "lehrer", "klassenzimmer", "lektion", "lernen", "buch", "universität", "wissen",
		// Portuguese
		"educação", "escola", "estudante", "professor", "sala de aula", "lição", "estudar", "livro", "universidade", "aprendizagem",
		// Romanian
		"educație", "școală", "student", "profesor", "clasă", "lecție", "studiu", "carte", "universitate", "învățare",
	}, "education", 0.78},

	// === SCIENCE (100 clips) ===
	{[]string{
		// Italian
		"scienza", "science", "laboratorio", "ricerca", "esperimento", "scienziato", "microscopio", "provetta", "dati scientifici", "analisi", "scoperta",
		// English
		"science", "laboratory", "research", "experiment", "scientist", "microscope", "test tube", "data", "analysis", "discovery", "lab",
		// French
		"science", "laboratoire", "recherche", "expérience", "scientifique", "microscope", "éprouvette", "données", "découverte",
		// Spanish
		"ciencia", "laboratorio", "investigación", "experimento", "científico", "microscopio", "tubo de ensayo", "datos", "descubrimiento",
		// German
		"wissenschaft", "labor", "forschung", "experiment", "wissenschaftler", "mikroskop", "reagenzglas", "daten", "entdeckung",
		// Portuguese
		"ciência", "laboratório", "pesquisa", "experimento", "cientista", "microscópio", "tubo de ensaio", "dados", "descoberta",
		// Romanian
		"știință", "laborator", "cercetare", "experiment", "om de știință", "microscop", "eprubetă", "date", "descoperire",
	}, "science", 0.78},
}
