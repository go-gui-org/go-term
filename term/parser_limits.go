package term

// maxOSCBytes caps the OSC payload size so a malicious or runaway
// stream can't grow p.osc without bound. Real titles are tiny;
// anything beyond this is truncated and the rest of the OSC is
// silently swallowed up to its terminator.
const maxOSCBytes = 4096

// maxOSC1337Bytes is the enlarged cap for OSC 1337 (iTerm2 inline images
// and File= transfers). Payloads are base64-encoded so a 50 KB PNG becomes
// ~67 KB on the wire; 32 MiB leaves room for real file downloads (~24 MiB
// of file data) while keeping a runaway stream bounded. Exceeding it sets
// p.oscTrunc, which drops the sequence outright rather than handing a
// truncated payload to the decoder.
const maxOSC1337Bytes = 32 << 20

// maxDCSBytes caps DCS payloads, which in practice means Sixel frames.
// Sixel costs roughly 1.7 bytes per pixel at photographic densities
// (measured: chafa 1.18 emits 1.6 MB for a 1200×800 image), so the old
// 1 MiB cap truncated any full-window picture — `chafa shot.png` in a
// 160×45 pane rendered as the top half of the image. The decoder rejects
// frames beyond maxSixelWidth×maxSixelHeight, and 4096×4096 at that
// density is ~28 MB, so 32 MiB clears the largest frame the decoder will
// accept from a real encoder. (Sixel has no upper bound on bytes per
// pixel — a stream that re-selects a color register between every pixel
// can still be cut short — but that is a synthetic shape, not one an
// encoder produces.) Matches the OSC 1337 limit.
const maxDCSBytes = 32 << 20

// maxDCSRetain bounds the DCS buffer kept between sequences. A sixel frame
// can grow p.dcs to maxDCSBytes; keeping that array alive would pin tens of
// MB per pane for the session, while dropping it after every frame would
// re-allocate on each frame of a sixel animation. Retaining up to 4 MiB
// covers ordinary frames (a full-window chafa image is ~1.6 MB) and releases
// only the outliers.
const maxDCSRetain = 4 << 20

// maxAPCBytes caps a single APC escape payload. Kitty Graphics Protocol
// recommends ≤4096 base64 chars per chunk (~3 KB decoded); 8 KB is plenty.
const maxAPCBytes = 8192

// maxKittyImageBytes caps the assembled (pre-decode) base64 text for a single
// KGP image across all chunks. Matches the iTerm2 OSC 1337 limit.
const maxKittyImageBytes = 4 << 20

// maxKittyPendingChunks caps concurrent in-flight KGP chunked transmissions
// (m=1 sequences that have not yet received a finalising m=0). Bounds
// kittyChunks growth from a stream that opens many IDs and never closes them.
const maxKittyPendingChunks = 64

// maxKittyStoreEntries caps the off-screen image store. When full, all
// stored images are evicted and their temp files removed before adding the
// new entry, bounding both memory and disk usage.
const maxKittyStoreEntries = 256

// da1Reply is the Primary Device Attribute response: VT100 with advanced
// video (1;2) plus Sixel graphics (extension 4). Apps like fish probe with
// CSI c at startup and stall briefly waiting for it; sixel-aware apps and
// ucs-detect read extension 4 to confirm Sixel support (term has a decoder).
var da1Reply = []byte("\x1b[?1;2;4c")

// da2Reply is the Secondary Device Attribute response: no terminal
// version, firmware version, or hardware options.
var da2Reply = []byte("\x1b[>0;0;0c")

// xtversionReply answers XTVERSION (CSI > q): DCS > | name(version) ST.
// blessed/ucs-detect parse the parenthesized form into name + version.
var xtversionReply = []byte("\x1bP>|go-term(" + termVersion + ")\x1b\\")

// termVersion is the advertised software version (XTVERSION). Bump on release.
const termVersion = "0.1"

// maxCSIParams caps the SGR/CSI parameter list to bound memory use against
// pathological streams like "\x1b[1;1;1;...m".
const maxCSIParams = 32

// maxCSIParamValue caps a single accumulated parameter so a digit-only
// run "\x1b[99999...9m" can't overflow int. Real terminals never need
// values above this.
const maxCSIParamValue = 1 << 20

// maxTitleStack caps the XTWINOPS title stack, matching xterm's limit. Pushes
// past the cap are dropped rather than evicting the oldest entry: an app that
// pushes without popping is misbehaving, and dropping keeps the *first* title
// (the shell's) recoverable.
const maxTitleStack = 10

// maxXTGETTCAPParts caps the number of capability names in one XTGETTCAP
// query so a pathological DCS (4096 semicolons) can't force a large
// allocation or iteration. Real apps query 1–3 caps at a time.
const maxXTGETTCAPParts = 32
