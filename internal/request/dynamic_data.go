package request

// The sample vocabularies below are deliberately small and reviewable. They
// keep the shapes and naming style of the @faker-js/faker 5.5.3 English
// locale without embedding its full data set. Postman's variable names and
// output formats are the compatibility target; the exact words are not.

type currency struct {
	Code   string
	Name   string
	Symbol string
}

type ibanPart struct {
	Kind  byte // 'a' letters, 'n' digits, 'c' letters or digits
	Count int
}

type ibanFormat struct {
	Country string
	Parts   []ibanPart
}

var (
	firstNames = []string{
		"Aaron", "Alex", "Alia", "Amelia", "Anais", "Armando", "Chandler",
		"Casey", "Connie", "Daniel", "Donnell", "Emma", "Ethan", "Gracie", "Grace",
		"Gwen", "Henry", "Isla", "Jack", "Jaden", "Jakob", "Jarrell", "Jonathon",
		"Kendrick", "Leo", "Lottie", "Maya", "Megane", "Mia", "Noah", "Nora",
		"Olivia", "Owen", "Pablo", "Quinten", "Ruby", "Ruthe", "Samuel", "Sofia",
		"Stella", "Sylvan", "Talon", "Theo", "Trevor", "Tristin", "William", "Zoe",
	}
	lastNames = []string{
		"Abbott", "Abernathy", "Brown", "Chen", "Fay", "Garcia", "Grady",
		"Gutkowski", "Harvey", "Johns", "Kassulke", "Khan", "Kim", "Kerluke", "Kovacek",
		"Kuhic", "Kunze", "Leffler", "Martin", "Monahan", "Patel",
		"Runolfsdottir", "Schaden", "Schneider", "Silva", "Smith", "Smitham",
		"Taylor", "Vandervort", "Wilmer", "Willms",
	}
	cityPrefixes = []string{"North", "East", "West", "South", "New", "Lake", "Port"}
	citySuffixes = []string{
		"town", "ton", "land", "ville", "berg", "burgh", "borough", "bury",
		"view", "port", "mouth", "stad", "furt", "chester", "fort", "haven",
		"side", "shire",
	}
	cityNames = []string{
		"San Francisco", "New York", "Austin", "Seattle", "Boston", "Chicago",
		"Denver", "Portland", "Miami", "Dallas", "Phoenix", "Atlanta",
	}
	streetSuffixes = []string{
		"Alley", "Avenue", "Branch", "Bridge", "Brook", "Burg", "Bypass",
		"Canyon", "Cape", "Causeway", "Center", "Circle", "Cliff", "Club",
		"Common", "Corner", "Court", "Cove", "Creek", "Crescent", "Crest",
		"Crossing", "Dale", "Drive", "Estate", "Falls", "Ferry", "Field",
		"Ford", "Forest", "Forge", "Fork", "Fort", "Garden", "Gateway",
		"Glen", "Green", "Grove", "Harbor", "Haven", "Heights", "Highway",
		"Hill", "Hollow", "Island", "Junction", "Key", "Lake", "Landing",
		"Lane", "Light", "Loop", "Mall", "Manor", "Meadow", "Mews", "Mill",
		"Mission", "Mountain", "Orchard", "Oval", "Park", "Parkway", "Pass",
		"Path", "Pike", "Pine", "Place", "Plain", "Plaza", "Point", "Port",
		"Prairie", "Ranch", "Rapids", "Ridge", "River", "Road", "Route",
		"Row", "Run", "Shore", "Skyway", "Spring", "Springs", "Spur",
		"Square", "Station", "Stream", "Street", "Summit", "Terrace",
		"Trace", "Track", "Trail", "Tunnel", "Turnpike", "Union", "Valley",
		"View", "Village", "Ville", "Vista", "Walk", "Wall", "Way", "Well",
	}
	countries = []string{
		"Afghanistan", "Albania", "Algeria", "Argentina", "Australia",
		"Austria", "Bangladesh", "Belgium", "Brazil", "Canada", "Chile",
		"China", "Colombia", "Croatia", "Czech Republic", "Denmark", "Egypt",
		"Estonia", "Finland", "France", "Germany", "Ghana", "Greece",
		"Hungary", "Iceland", "India", "Indonesia", "Ireland", "Israel",
		"Italy", "Jamaica", "Japan", "Kazakhstan", "Kenya",
		"Lao People's Democratic Republic", "Malaysia", "Mexico", "Morocco",
		"Netherlands", "New Zealand", "Nigeria", "Norway", "Pakistan",
		"Peru", "Philippines", "Poland", "Portugal", "Romania", "Saudi Arabia",
		"Singapore", "South Africa", "South Korea", "Spain", "Sweden",
		"Switzerland", "Thailand", "Turkey", "Ukraine",
		"United Arab Emirates", "United Kingdom", "United States", "Vietnam",
	}
	countryCodes = []string{
		"AD", "AE", "AF", "AL", "AM", "AO", "AR", "AT", "AU", "AZ", "BA",
		"BD", "BE", "BG", "BH", "BR", "BY", "CA", "CH", "CL", "CN", "CO",
		"CR", "CU", "CV", "CY", "CZ", "DE", "DK", "DO", "DZ", "EC", "EE",
		"EG", "ES", "FI", "FR", "GB", "GE", "GH", "GR", "GT", "HK", "HN",
		"HR", "HU", "ID", "IE", "IL", "IN", "IQ", "IR", "IS", "IT", "JM",
		"JO", "JP", "KE", "KG", "KH", "KR", "KW", "KZ", "LA", "LB", "LK",
		"LT", "LU", "LV", "MA", "MD", "ME", "MK", "MM", "MN", "MT", "MX",
		"MY", "NG", "NL", "NO", "NP", "NZ", "OM", "PA", "PE", "PH", "PK",
		"PL", "PT", "PY", "QA", "RO", "RS", "RU", "SA", "SE", "SG", "SI",
		"SK", "SN", "SV", "TH", "TN", "TR", "TW", "TZ", "UA", "UG", "US",
		"UY", "UZ", "VE", "VN", "YE", "ZA", "ZM", "ZW",
	}
	freeEmailProviders    = []string{"gmail.com", "yahoo.com", "hotmail.com"}
	exampleEmailProviders = []string{"example.org", "example.com", "example.net"}
	domainSuffixes        = []string{"com", "biz", "info", "name", "net", "org"}
	protocols             = []string{"http", "https"}
	accountTypes          = []string{
		"Checking", "Savings", "Money Market", "Investment", "Home Loan",
		"Credit Card", "Auto Loan", "Personal Loan",
	}
	transactionTypes = []string{"deposit", "withdrawal", "payment", "invoice"}
	currencies       = []currency{
		{"AED", "UAE Dirham", "د.إ"},
		{"AFN", "Afghani", "؋"},
		{"ALL", "Lek", "Lek"},
		{"ARS", "Argentine Peso", "$"},
		{"AUD", "Australian Dollar", "$"},
		{"BGN", "Bulgarian Lev", "лв"},
		{"BOB", "Boliviano boliviano", "Bs"},
		{"BRL", "Brazilian Real", "R$"},
		{"CAD", "Canadian Dollar", "$"},
		{"CDF", "Congolese Franc", "FC"},
		{"CHF", "Swiss Franc", "CHF"},
		{"CNY", "Yuan Renminbi", "¥"},
		{"CZK", "Czech Koruna", "Kč"},
		{"DKK", "Danish Krone", "kr"},
		{"EUR", "Euro", "€"},
		{"GBP", "Pound Sterling", "£"},
		{"GNF", "Guinea Franc", "FG"},
		{"HKD", "Hong Kong Dollar", "$"},
		{"HUF", "Forint", "Ft"},
		{"INR", "Indian Rupee", "₹"},
		{"JPY", "Yen", "¥"},
		{"KRW", "Won", "₩"},
		{"MXN", "Mexican Peso", "$"},
		{"NIO", "Cordoba Oro", "C$"},
		{"NOK", "Norwegian Krone", "kr"},
		{"NZD", "New Zealand Dollar", "$"},
		{"PLN", "Polish Zloty", "zł"},
		{"RUB", "Russian Ruble", "₽"},
		{"SEK", "Swedish Krona", "kr"},
		{"SGD", "Singapore Dollar", "$"},
		{"USD", "United States Dollar", "$"},
		{"XPF", "CFP Franc", "₣"},
		{"ZAR", "South African Rand", "R"},
		{"ZMK", "Zambian Kwacha", "ZK"},
	}
	companySuffixes   = []string{"Inc", "and Sons", "LLC", "Group"}
	companyAdjectives = []string{
		"Adaptive", "Advanced", "Automated", "Balanced", "Business-focused",
		"Centralized", "Configurable", "Customer-focused", "Decentralized",
		"Digitized", "Distributed", "Enhanced", "Enterprise-wide", "Ergonomic",
		"Down-sized", "Future-proofed", "Innovative", "Integrated", "Intuitive",
		"Proactive", "Robust", "Seamless", "Self-enabling", "Synergistic",
		"Versatile", "Virtual", "Visionary",
	}
	companyDescriptors = []string{
		"24/7", "asynchronous", "bandwidth-monitored", "client-driven",
		"client-server", "coherent", "composite", "context-sensitive",
		"dynamic", "global", "heuristic", "high-level", "holistic",
		"interactive", "mission-critical", "mobile", "modular", "multi-tasking",
		"next generation", "optimal", "real-time", "scalable", "stable",
		"static", "user-facing", "value-added", "zero tolerance",
	}
	companyNouns = []string{
		"ability", "access", "adapter", "algorithm", "alliance", "application",
		"architecture", "archive", "bandwidth", "capability", "collaboration",
		"database", "encryption", "framework", "interface", "internet solution",
		"intranet", "knowledge base", "matrix", "methodology", "middleware",
		"migration", "open architecture", "orchestration", "paradigm",
		"installation", "protocol", "secured line", "software", "solution",
		"strategy", "superstructure", "support", "synergy", "system",
		"system engine", "throughput", "toolset", "website", "workforce",
	}
	bsAdjectives = []string{
		"24/7", "24/365", "B2B", "B2C", "back-end", "best-of-breed",
		"bleeding-edge", "collaborative", "cross-platform", "cutting-edge",
		"dynamic", "end-to-end", "enterprise", "extensible", "frictionless",
		"front-end", "granular", "innovative", "integrated", "killer",
		"magnetic", "mission-critical", "next-generation", "open-source",
		"proactive", "real-time", "scalable", "seamless", "sticky", "strategic",
		"synergistic", "transparent", "value-added", "viral", "virtual",
		"visionary", "web-enabled",
	}
	bsBuzzes = []string{
		"aggregate", "architect", "benchmark", "brand", "cultivate", "deliver",
		"deploy", "drive", "embrace", "empower", "enable", "engage", "enhance",
		"evolve", "expedite", "exploit", "extend", "generate", "grow",
		"harness", "implement", "incentivize", "innovate", "integrate",
		"iterate", "leverage", "maximize", "mesh", "monetize", "optimize",
		"orchestrate", "redefine", "reinvent", "repurpose", "revolutionize",
		"scale", "seize", "strategize", "streamline", "synergize", "synthesize",
		"target", "transform", "transition", "unleash", "utilize", "visualize",
	}
	bsNouns = []string{
		"applications", "architectures", "bandwidth", "channels", "communities",
		"content", "convergence", "deliverables", "e-business", "e-commerce",
		"e-markets", "e-services", "e-tailers", "experiences", "functionalities",
		"infrastructures", "interfaces", "markets", "metrics", "mindshare",
		"models", "networks", "niches", "paradigms", "partnerships", "platforms",
		"portals", "ROI", "schemas", "solutions", "supply-chains", "synergies",
		"systems", "technologies", "users", "vortals", "web-readiness",
		"web services",
	}
	catchPhraseAdjectives = []string{
		"Adaptive", "Advanced", "Automated", "Balanced", "Business-focused",
		"Centralized", "Configurable", "Cross-platform", "Customer-focused",
		"Decentralized", "Digitized", "Distributed", "Enhanced", "Ergonomic",
		"Down-sized", "Future-proofed", "Innovative", "Integrated", "Intuitive",
		"Proactive", "Robust", "Seamless", "Self-enabling", "Synergistic",
		"Versatile", "Virtual", "Visionary",
	}
	catchPhraseDescriptors = []string{
		"24 hour", "24/7", "3rd generation", "4th generation", "5th generation",
		"6th generation", "asynchronous", "bandwidth-monitored", "bi-directional",
		"client-driven", "client-server", "coherent", "composite",
		"context-sensitive", "dedicated", "dynamic", "global", "heuristic",
		"high-level", "holistic", "homogeneous", "interactive", "mission-critical",
		"mobile", "modular", "multi-tasking", "needs-based", "next generation",
		"optimal", "real-time", "scalable", "stable", "static", "systematic",
		"user-facing", "value-added", "zero tolerance",
	}
	catchPhraseNouns = []string{
		"ability", "access", "adapter", "algorithm", "alliance", "application",
		"architecture", "archive", "bandwidth", "capability", "collaboration",
		"database", "encryption", "framework", "interface", "intranet",
		"knowledge base", "matrix", "methodology", "middleware", "migration",
		"open architecture", "orchestration", "paradigm", "protocol",
		"installation", "secured line", "software", "solution", "strategy",
		"superstructure", "support", "synergy", "system", "system engine",
		"throughput", "toolset", "website", "workforce",
	}
	colors = []string{
		"red", "green", "blue", "yellow", "purple", "mint green", "teal",
		"white", "black", "orange", "pink", "grey", "maroon", "violet",
		"turquoise", "tan", "sky blue", "salmon", "plum", "orchid", "olive",
		"magenta", "lime", "ivory", "indigo", "gold", "fuchsia", "cyan",
		"azure", "lavender", "silver",
	}
	departments = []string{
		"Books", "Movies", "Music", "Games", "Electronics", "Computers", "Home",
		"Garden", "Tools", "Grocery", "Health", "Beauty", "Toys", "Kids",
		"Baby", "Clothing", "Shoes", "Jewelery", "Sports", "Outdoors",
		"Automotive", "Industrial",
	}
	productAdjectives = []string{
		"Small", "Ergonomic", "Rustic", "Intelligent", "Gorgeous", "Incredible",
		"Fantastic", "Practical", "Sleek", "Awesome", "Generic", "Handcrafted",
		"Handmade", "Licensed", "Refined", "Unbranded", "Tasty",
	}
	productMaterials = []string{
		"Steel", "Wooden", "Concrete", "Plastic", "Cotton", "Granite", "Rubber",
		"Metal", "Soft", "Fresh", "Frozen",
	}
	products = []string{
		"Chair", "Car", "Computer", "Keyboard", "Mouse", "Bike", "Ball",
		"Gloves", "Pants", "Shirt", "Table", "Shoes", "Hat", "Towels", "Soap",
		"Tuna", "Chicken", "Fish", "Cheese", "Bacon", "Pizza", "Salad",
		"Sausages", "Chips",
	}
	databaseColumns = []string{
		"id", "title", "name", "email", "phone", "token", "group", "category",
		"password", "comment", "avatar", "status", "createdAt", "updatedAt",
	}
	databaseTypes = []string{
		"int", "varchar", "text", "date", "datetime", "tinyint", "time",
		"timestamp", "smallint", "mediumint", "bigint", "decimal", "float",
		"double", "real", "bit", "boolean", "serial", "blob", "binary", "enum",
		"set", "geometry", "point",
	}
	databaseCollations = []string{
		"utf8_unicode_ci", "utf8_general_ci", "utf8_bin", "ascii_bin",
		"ascii_general_ci", "cp1250_bin", "cp1250_general_ci",
	}
	databaseEngines = []string{
		"InnoDB", "MyISAM", "MEMORY", "CSV", "BLACKHOLE", "ARCHIVE",
	}
	namePrefixes = []string{"Mr.", "Mrs.", "Ms.", "Miss", "Dr."}
	nameSuffixes = []string{
		"Jr.", "Sr.", "I", "II", "III", "IV", "V", "MD", "DDS", "PhD", "DVM",
	}
	jobDescriptors = []string{
		"Lead", "Senior", "Direct", "Corporate", "Dynamic", "Future", "Product",
		"National", "Regional", "District", "Central", "Global", "Customer",
		"Investor", "International", "Legacy", "Forward", "Internal", "Human",
		"Chief", "Principal",
	}
	jobAreas = []string{
		"Solutions", "Program", "Brand", "Security", "Research", "Marketing",
		"Directives", "Implementation", "Integration", "Functionality",
		"Response", "Paradigm", "Tactics", "Identity", "Markets", "Group",
		"Division", "Applications", "Optimization", "Operations",
		"Infrastructure", "Intranet", "Communications", "Web", "Branding",
		"Quality", "Assurance", "Mobility", "Accounts", "Data", "Creative",
		"Configuration", "Accountability", "Interactions", "Factors",
		"Usability", "Metrics",
	}
	jobTypes = []string{
		"Supervisor", "Associate", "Executive", "Liaison", "Officer", "Manager",
		"Engineer", "Specialist", "Director", "Coordinator", "Administrator",
		"Architect", "Analyst", "Designer", "Planner", "Orchestrator",
		"Technician", "Developer", "Producer", "Consultant", "Assistant",
		"Facilitator", "Agent", "Representative", "Strategist",
	}
	hackerAbbreviations = []string{
		"TCP", "HTTP", "SDD", "RAM", "GB", "CSS", "SSL", "AGP", "SQL", "FTP",
		"PCI", "AI", "ADP", "RSS", "XML", "EXE", "COM", "HDD", "THX", "SMTP",
		"SMS", "USB", "PNG", "SAS", "IB", "SCSI", "JSON", "XSS", "JBOD",
	}
	hackerAdjectives = []string{
		"auxiliary", "primary", "back-end", "digital", "open-source", "virtual",
		"cross-platform", "redundant", "online", "haptic", "multi-byte",
		"bluetooth", "wireless", "1080p", "neural", "optical", "solid state",
		"mobile",
	}
	hackerNouns = []string{
		"driver", "protocol", "bandwidth", "panel", "microchip", "program",
		"port", "card", "array", "interface", "system", "sensor", "firewall",
		"hard drive", "pixel", "alarm", "feed", "monitor", "application",
		"transmitter", "bus", "circuit", "capacitor", "matrix",
	}
	hackerVerbs = []string{
		"back up", "bypass", "hack", "override", "compress", "copy", "navigate",
		"index", "connect", "generate", "quantify", "calculate", "synthesize",
		"input", "transmit", "program", "reboot", "parse",
	}
	hackerIngverbs = []string{
		"backing up", "bypassing", "hacking", "overriding", "compressing",
		"copying", "navigating", "indexing", "connecting", "generating",
		"quantifying", "calculating", "synthesizing", "transmitting",
		"programming", "parsing",
	}
	hackerPhrases = []string{
		"If we {{verb}} the {{noun}}, we can get to the {{abbreviation}} {{noun}} through the {{adjective}} {{abbreviation}} {{noun}}!",
		"We need to {{verb}} the {{adjective}} {{abbreviation}} {{noun}}!",
		"Try to {{verb}} the {{abbreviation}} {{noun}}, maybe it will {{verb}} the {{adjective}} {{noun}}!",
		"You can't {{verb}} the {{noun}} without {{ingverb}} the {{adjective}} {{abbreviation}} {{noun}}!",
		"Use the {{adjective}} {{abbreviation}} {{noun}}, then you can {{verb}} the {{adjective}} {{noun}}!",
		"The {{abbreviation}} {{noun}} is down, {{verb}} the {{adjective}} {{noun}} so we can {{verb}} the {{abbreviation}} {{noun}}!",
		"{{ingverb}} the {{noun}} won't do anything, we need to {{verb}} the {{adjective}} {{abbreviation}} {{noun}}!",
		"I'll {{verb}} the {{adjective}} {{abbreviation}} {{noun}}, that should {{noun}} the {{abbreviation}} {{noun}}!",
	}
	loremWords = []string{
		"alias", "consequatur", "aut", "perferendis", "sit", "voluptatem",
		"accusantium", "doloremque", "aperiam", "eaque", "ipsa", "quae", "ab",
		"illo", "inventore", "veritatis", "et", "quasi", "architecto",
		"beatae", "vitae", "dicta", "sunt", "explicabo", "aspernatur", "odit",
		"fugit", "sed", "quia", "consequuntur", "magni", "dolores", "eos",
		"qui", "ratione", "sequi", "nesciunt", "neque", "dolorem", "ipsum",
		"dolor", "amet", "consectetur", "adipisci", "velit", "non", "numquam",
		"eius", "modi", "tempora", "incidunt", "ut", "labore", "dolore",
		"magnam", "aliquam", "quaerat", "enim", "ad", "minima", "veniam",
		"quis", "nostrum", "exercitationem", "ullam", "corporis", "nemo",
		"ipsam", "voluptas", "suscipit", "laboriosam", "nisi", "aliquid",
		"ex", "ea", "commodi", "autem", "vel", "eum", "iure", "reprehenderit",
		"in", "voluptate", "esse", "quam", "nihil", "molestiae", "iusto",
		"odio", "dignissimos", "ducimus", "blanditiis", "praesentium",
		"laudantium", "totam", "rem", "voluptatum", "deleniti", "atque",
		"corrupti", "quos", "quas", "molestias", "excepturi", "sint",
		"occaecati", "cupiditate", "provident", "perspiciatis", "unde",
		"omnis", "iste", "repellat", "nobis",
	}
	fileTypes = []string{
		"application", "audio", "chemical", "font", "image", "message",
		"model", "multipart", "text", "video",
	}
	commonFileTypes = []string{"video", "audio", "image", "text", "application"}
	fileExtensions  = []string{
		"gdoc", "war", "book", "fsc", "cpio", "m2v", "wav", "png", "pdf",
		"json", "txt", "csv", "zip", "mp4", "mp3", "jpg", "jpeg", "gif", "svg",
		"html", "xml", "yaml", "exe", "iso", "tar", "gz", "7z", "doc", "docx",
		"xls", "xlsx", "ppt", "pptx", "epub", "apk", "bin", "dat", "db", "log",
		"md", "rtf", "tiff", "webm",
	}
	commonFileExtensions = []string{
		"pdf", "mpeg", "m2v", "mpg4", "mp3", "wav", "png", "jpg", "jpeg", "gif",
		"mp4", "html", "txt", "csv", "json", "zip",
	}
	mimeTypes = []string{
		"audio/vnd.vmx.cvsd", "application/pdf", "audio/mpeg", "audio/wav",
		"image/png", "image/jpeg", "image/gif", "video/mp4", "video/mpeg",
		"text/html", "application/json", "text/plain", "application/zip",
		"image/svg+xml", "font/woff2", "model/gltf+json", "message/http",
		"multipart/form-data", "chemical/x-cdx", "application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}
	directoryPaths = []string{
		"/Applications", "/bin", "/boot", "/boot/defaults", "/dev", "/etc",
		"/etc/defaults", "/etc/mail", "/etc/namedb", "/etc/periodic",
		"/etc/ppp", "/home", "/home/user", "/home/user/dir", "/lib",
		"/Library", "/lost+found", "/media", "/mnt", "/net", "/Network",
		"/opt", "/opt/bin", "/opt/include", "/opt/lib", "/opt/sbin",
		"/opt/share", "/private", "/private/tmp", "/private/var", "/proc",
		"/rescue", "/root", "/sbin", "/selinux", "/srv", "/sys", "/System",
		"/tmp", "/Users", "/usr", "/usr/X11R6", "/usr/bin", "/usr/include",
		"/usr/lib", "/usr/libdata", "/usr/libexec", "/usr/local/bin",
		"/usr/local/src", "/usr/obj", "/usr/ports", "/usr/sbin", "/usr/share",
		"/usr/src", "/var", "/var/log", "/var/mail", "/var/spool",
		"/var/tmp", "/var/yp",
	}
	// Postman's LOCALES list, copied from
	// postman-collection/lib/superstring/dynamic-variables.js.
	locales = []string{
		"af", "am", "an", "ar", "ast", "az", "be", "bg", "bh", "bn", "br",
		"bs", "ca", "ceb", "ckb", "co", "cs", "cy", "da", "de", "el", "en",
		"eo", "es", "et", "eu", "fa", "fi", "fil", "fo", "fr", "fy", "ga",
		"gd", "gl", "gn", "gu", "ha", "haw", "he", "hi", "hmn", "hr", "ht",
		"hu", "hy", "ia", "id", "ig", "is", "it", "ja", "jv", "ka", "kk",
		"km", "kn", "ko", "ku", "ky", "la", "lb", "ln", "lo", "lt", "lv",
		"mg", "mi", "mk", "ml", "mn", "mo", "mr", "ms", "mt", "my", "nb",
		"ne", "nl", "nn", "no", "ny", "oc", "om", "or", "pa", "pl", "ps",
		"pt", "qu", "rm", "ro", "ru", "sd", "sh", "si", "sk", "sl", "sm",
		"sn", "so", "sq", "sr", "st", "su", "sv", "sw", "ta", "te", "tg",
		"th", "ti", "tk", "to", "tr", "tt", "tw", "ug", "uk", "ur", "uz",
		"vi", "wa", "xh", "yi", "yo", "zh", "zu",
	}
	weekdays = []string{
		"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday",
		"Saturday",
	}
	months = []string{
		"January", "February", "March", "April", "May", "June", "July",
		"August", "September", "October", "November", "December",
	}
	ibanFormats = []ibanFormat{
		{"AL", []ibanPart{{'n', 8}, {'c', 16}}},
		{"AD", []ibanPart{{'n', 8}, {'c', 12}}},
		{"AT", []ibanPart{{'n', 5}, {'n', 11}}},
		{"AZ", []ibanPart{{'a', 4}, {'n', 20}}},
		{"BH", []ibanPart{{'a', 4}, {'c', 14}}},
		{"BE", []ibanPart{{'n', 3}, {'n', 9}}},
		{"BA", []ibanPart{{'n', 6}, {'n', 10}}},
		{"BR", []ibanPart{{'n', 13}, {'n', 10}, {'a', 1}, {'c', 1}}},
		{"BG", []ibanPart{{'a', 4}, {'n', 6}, {'c', 8}}},
		{"CR", []ibanPart{{'n', 4}, {'n', 14}}},
		{"HR", []ibanPart{{'n', 17}}},
		{"CY", []ibanPart{{'n', 8}, {'c', 16}}},
		{"CZ", []ibanPart{{'n', 20}}},
		{"DK", []ibanPart{{'n', 14}}},
		{"EE", []ibanPart{{'n', 16}}},
		{"FI", []ibanPart{{'n', 14}}},
		{"FR", []ibanPart{{'n', 10}, {'c', 11}, {'n', 2}}},
		{"DE", []ibanPart{{'n', 18}}},
		{"GR", []ibanPart{{'n', 7}, {'c', 16}}},
		{"HU", []ibanPart{{'n', 24}}},
		{"IE", []ibanPart{{'a', 4}, {'n', 14}}},
		{"IT", []ibanPart{{'a', 1}, {'n', 10}, {'c', 12}}},
		{"LV", []ibanPart{{'a', 4}, {'c', 13}}},
		{"LT", []ibanPart{{'n', 16}}},
		{"LU", []ibanPart{{'n', 3}, {'c', 13}}},
		{"MT", []ibanPart{{'a', 4}, {'n', 5}, {'c', 18}}},
		{"MU", []ibanPart{{'a', 4}, {'n', 4}, {'n', 15}, {'a', 3}}},
		{"NL", []ibanPart{{'a', 4}, {'n', 10}}},
		{"NO", []ibanPart{{'n', 11}}},
		{"PL", []ibanPart{{'n', 8}, {'n', 16}}},
		{"PT", []ibanPart{{'n', 21}}},
		{"RO", []ibanPart{{'a', 4}, {'c', 16}}},
		{"SK", []ibanPart{{'n', 20}}},
		{"SI", []ibanPart{{'n', 15}}},
		{"ES", []ibanPart{{'n', 20}}},
		{"SE", []ibanPart{{'n', 20}}},
		{"CH", []ibanPart{{'n', 5}, {'c', 12}}},
		{"GB", []ibanPart{{'a', 4}, {'n', 14}}},
	}
)

const (
	digits         = "0123456789"
	alphanumeric   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upperAlpha     = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	base58Alphabet = "123456789abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"
)
