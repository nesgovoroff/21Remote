// SPDX-License-Identifier: AGPL-3.0-or-later
// 21 Remote — CoreMIDI bridge between macOS MIDI and the remote21-core engine.
//
// Publishes the virtual endpoints "21 Remote" (a source and a destination that
// DAWs see), listens to every MIDI input on the system and forwards everything to
// the engine as text lines on a pipe:
//
//   to engine:   S <status text> | D | I <uid> <name> | O <uid> <name> | E | M <uid|V> <hex bytes>
//   from engine: L (rescan devices) | T <uid|V> <hex bytes> (transmit)
//
// Declarations are written out by hand so no macOS SDK is needed to build it.
#include <stdint.h>
#include <stdbool.h>

typedef int32_t OSStatus;
typedef uint32_t MIDIObjectRef, MIDIClientRef, MIDIPortRef, MIDIEndpointRef;
typedef const void *CFStringRef;

#pragma pack(push, 4)
typedef struct { uint64_t timeStamp; uint16_t length; uint8_t data[256]; } MIDIPacket;
typedef struct { uint32_t numPackets; MIDIPacket packet[1]; } MIDIPacketList;
#pragma pack(pop)
typedef struct { int32_t messageID; uint32_t messageSize; } MIDINotification;

typedef void (*MIDIReadProc)(const MIDIPacketList *, void *, void *);
typedef void (*MIDINotifyProc)(const MIDINotification *, void *);

extern OSStatus MIDIClientCreate(CFStringRef, MIDINotifyProc, void *, MIDIClientRef *);
extern OSStatus MIDIInputPortCreate(MIDIClientRef, CFStringRef, MIDIReadProc, void *, MIDIPortRef *);
extern OSStatus MIDIOutputPortCreate(MIDIClientRef, CFStringRef, MIDIPortRef *);
extern OSStatus MIDISourceCreate(MIDIClientRef, CFStringRef, MIDIEndpointRef *);
extern OSStatus MIDIDestinationCreate(MIDIClientRef, CFStringRef, MIDIReadProc, void *, MIDIEndpointRef *);
extern unsigned long MIDIGetNumberOfSources(void);
extern MIDIEndpointRef MIDIGetSource(unsigned long);
extern unsigned long MIDIGetNumberOfDestinations(void);
extern MIDIEndpointRef MIDIGetDestination(unsigned long);
extern OSStatus MIDIPortConnectSource(MIDIPortRef, MIDIEndpointRef, void *);
extern OSStatus MIDIPortDisconnectSource(MIDIPortRef, MIDIEndpointRef);
extern OSStatus MIDIObjectGetStringProperty(MIDIObjectRef, CFStringRef, CFStringRef *);
extern OSStatus MIDIObjectGetIntegerProperty(MIDIObjectRef, CFStringRef, int32_t *);
extern OSStatus MIDIObjectSetIntegerProperty(MIDIObjectRef, CFStringRef, int32_t);
extern OSStatus MIDISend(MIDIPortRef, MIDIEndpointRef, const MIDIPacketList *);
extern OSStatus MIDIReceived(MIDIEndpointRef, const MIDIPacketList *);
extern MIDIPacket *MIDIPacketListInit(MIDIPacketList *);
extern MIDIPacket *MIDIPacketListAdd(MIDIPacketList *, unsigned long, MIDIPacket *, uint64_t, unsigned long, const uint8_t *);
extern const CFStringRef kMIDIPropertyDisplayName;
extern const CFStringRef kMIDIPropertyUniqueID;

extern CFStringRef CFStringCreateWithCString(const void *, const char *, uint32_t);
extern bool CFStringGetCString(CFStringRef, char *, long, uint32_t);
extern void CFRelease(const void *);
#define UTF8 0x08000100u

typedef void *pthread_t;
typedef struct { long sig; char opaque[56]; } pthread_mutex_t;
extern int pthread_create(pthread_t *, const void *, void *(*)(void *), void *);
extern int pthread_mutex_lock(pthread_mutex_t *);
extern int pthread_mutex_unlock(pthread_mutex_t *);
extern long write(int, const void *, unsigned long);
extern long read(int, void *, unsigned long);
extern int snprintf(char *, unsigned long, const char *, ...);
extern long strtol(const char *, char **, int);

static int toCore = -1, fromCore = -1;
static pthread_mutex_t wmu = {0x32AAABA7, {0}};    // PTHREAD_MUTEX_INITIALIZER
static pthread_mutex_t scanmu = {0x32AAABA7, {0}};
static MIDIClientRef client;
static MIDIPortRef inPort, outPort;
static MIDIEndpointRef vSrc, vDst;

#define VIRTUAL_REF ((void *)1)

static void sendRaw(const char *s, unsigned long n) {
	pthread_mutex_lock(&wmu);
	while (n > 0) {
		long w = write(toCore, s, n);
		if (w <= 0) break;
		s += w;
		n -= (unsigned long)w;
	}
	pthread_mutex_unlock(&wmu);
}

static void sendLine(const char *s) {
	unsigned long n = 0;
	while (s[n]) n++;
	sendRaw(s, n);
}

static const char hexd[] = "0123456789abcdef";

static void forward(const char *src, const MIDIPacketList *pl) {
	static char line[2 * 65536 + 64]; // a packet holds up to 64 KB (long SysEx)
	const MIDIPacket *p = &pl->packet[0];
	for (uint32_t i = 0; i < pl->numPackets; i++) {
		int n = snprintf(line, sizeof line, "M %s ", src);
		for (uint32_t k = 0; k < p->length && n < (int)sizeof line - 3; k++) {
			line[n++] = hexd[p->data[k] >> 4];
			line[n++] = hexd[p->data[k] & 15];
		}
		line[n++] = '\n';
		sendRaw(line, (unsigned long)n);
		// MIDIPacketNext: packets are 4-byte aligned on ARM, packed on Intel
		uintptr_t next = (uintptr_t)&p->data[p->length];
#if defined(__aarch64__) || defined(__arm__)
		next = (next + 3) & ~(uintptr_t)3;
#endif
		p = (const MIDIPacket *)next;
	}
}

// from a hardware input; the connection refCon carries its unique ID
static void deviceRead(const MIDIPacketList *pl, void *readRef, void *srcRef) {
	char id[16];
	snprintf(id, sizeof id, "%d", (int)(intptr_t)srcRef);
	forward(id, pl);
}

// from the virtual destination (a DAW sending to "21 Remote")
static void virtualRead(const MIDIPacketList *pl, void *readRef, void *srcRef) { forward("V", pl); }

static int32_t uidOf(MIDIObjectRef o) {
	int32_t v = 0;
	MIDIObjectGetIntegerProperty(o, kMIDIPropertyUniqueID, &v);
	return v;
}

static void nameOf(MIDIObjectRef o, char *buf, long n) {
	CFStringRef s = 0;
	buf[0] = 0;
	if (MIDIObjectGetStringProperty(o, kMIDIPropertyDisplayName, &s) == 0 && s) {
		CFStringGetCString(s, buf, n, UTF8);
		CFRelease(s);
	}
	for (long i = 0; buf[i]; i++)
		if (buf[i] == '\n' || buf[i] == '\r') buf[i] = ' ';
	if (!buf[0]) snprintf(buf, (unsigned long)n, "MIDI device");
}

// (re)connect every source and report the device list
static void scan(void) {
	pthread_mutex_lock(&scanmu);
	char line[400], name[256];
	sendLine("D\n");
	unsigned long ns = MIDIGetNumberOfSources();
	for (unsigned long i = 0; i < ns; i++) {
		MIDIEndpointRef e = MIDIGetSource(i);
		if (!e || e == vSrc) continue;
		int32_t uid = uidOf(e);
		nameOf(e, name, sizeof name);
		MIDIPortDisconnectSource(inPort, e); // avoid double connections on rescans
		MIDIPortConnectSource(inPort, e, (void *)(intptr_t)uid);
		snprintf(line, sizeof line, "I %d %s\n", (int)uid, name);
		sendLine(line);
	}
	unsigned long nd = MIDIGetNumberOfDestinations();
	for (unsigned long i = 0; i < nd; i++) {
		MIDIEndpointRef e = MIDIGetDestination(i);
		if (!e || e == vDst) continue;
		nameOf(e, name, sizeof name);
		snprintf(line, sizeof line, "O %d %s\n", (int)uidOf(e), name);
		sendLine(line);
	}
	sendLine("E\n");
	pthread_mutex_unlock(&scanmu);
}

static void notify(const MIDINotification *m, void *ref) {
	if (m && m->messageID == 1 /* kMIDIMsgSetupChanged */) scan();
}

static int unhex(char c) {
	if (c >= '0' && c <= '9') return c - '0';
	if (c >= 'a' && c <= 'f') return c - 'a' + 10;
	if (c >= 'A' && c <= 'F') return c - 'A' + 10;
	return -1;
}

static void transmit(char *args) {
	char *sp = args;
	while (*sp && *sp != ' ') sp++;
	if (!*sp) return;
	*sp++ = 0;
	static uint8_t data[8192]; // SysEx layout messages for the JS plug-ins
	unsigned long n = 0;
	while (sp[0] && sp[1] && n < sizeof data) {
		int h = unhex(sp[0]), l = unhex(sp[1]);
		if (h < 0 || l < 0) break;
		data[n++] = (uint8_t)(h << 4 | l);
		sp += 2;
	}
	if (!n) return;
	static uint32_t buf[2100]; // 4-byte aligned packet list storage (8 KB + headers)
	MIDIPacketList *pl = (MIDIPacketList *)buf;
	MIDIPacket *p = MIDIPacketListInit(pl);
	p = MIDIPacketListAdd(pl, sizeof buf, p, 0, n, data);
	if (!p) return;
	if (args[0] == 'V' && args[1] == 0) {
		MIDIReceived(vSrc, pl);
		return;
	}
	int32_t want = (int32_t)strtol(args, 0, 10);
	unsigned long nd = MIDIGetNumberOfDestinations();
	for (unsigned long i = 0; i < nd; i++) {
		MIDIEndpointRef e = MIDIGetDestination(i);
		if (e && e != vDst && uidOf(e) == want) MIDISend(outPort, e, pl);
	}
}

static void *commandLoop(void *unused) {
	static char buf[32768];
	unsigned long len = 0;
	for (;;) {
		long r = read(fromCore, buf + len, sizeof buf - 1 - len);
		if (r <= 0) return 0;
		len += (unsigned long)r;
		unsigned long start = 0;
		for (unsigned long i = 0; i < len; i++) {
			if (buf[i] != '\n') continue;
			buf[i] = 0;
			char *l = buf + start;
			if (l[0] == 'L') scan();
			else if (l[0] == 'T' && l[1] == ' ') transmit(l + 2);
			start = i + 1;
		}
		unsigned long rest = len - start;
		for (unsigned long i = 0; i < rest; i++) buf[i] = buf[start + i];
		len = rest;
		if (len >= sizeof buf - 1) len = 0; // overlong garbage line
	}
}

static CFStringRef cfs(const char *s) { return CFStringCreateWithCString(0, s, UTF8); }

int midiStart(int to, int from) {
	toCore = to;
	fromCore = from;
	char line[200];
	OSStatus st = MIDIClientCreate(cfs("21 Remote"), notify, 0, &client);
	if (st == 0) st = MIDIInputPortCreate(client, cfs("21 Remote In"), deviceRead, 0, &inPort);
	if (st == 0) st = MIDIOutputPortCreate(client, cfs("21 Remote Out"), &outPort);
	if (st == 0) st = MIDISourceCreate(client, cfs("21 Remote"), &vSrc);
	if (st == 0) st = MIDIDestinationCreate(client, cfs("21 Remote"), virtualRead, VIRTUAL_REF, &vDst);
	if (st != 0) {
		snprintf(line, sizeof line, "S MIDI is not available (CoreMIDI error %d).\n", (int)st);
		sendLine(line);
		return -1;
	}
	// fixed IDs so DAWs remember the port between launches (ignored if taken)
	MIDIObjectSetIntegerProperty(vSrc, kMIDIPropertyUniqueID, 0x4432316D);
	MIDIObjectSetIntegerProperty(vDst, kMIDIPropertyUniqueID, 0x44323172);
	sendLine("S MIDI ready. Other apps see the port \xE2\x80\x9C" "21 Remote\xE2\x80\x9D.\n");
	scan();
	pthread_t t;
	pthread_create(&t, 0, commandLoop, 0);
	return 0;
}
