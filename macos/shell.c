// SPDX-License-Identifier: AGPL-3.0-or-later
// 21 Remote — native macOS window shell.
//
// Starts the remote21-core engine that sits next to this executable, then shows its
// user interface in a WKWebView with a standard macOS menu bar. Written in C
// against the Objective-C runtime so it can be compiled without the macOS SDK.
//
// Closing the window only hides it: the app stays a normal running app (Dock,
// Cmd-Tab, Cmd-Q) with an icon in the menu bar (left click opens the window,
// right click shows a menu). Started with --background (as the DAW plug-ins
// do) it starts with the window hidden.
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

typedef void *id;
typedef void *SEL;
typedef void *Class;
typedef void *Protocol;
typedef struct { double x, y, w, h; } CGRect;
typedef struct { double w, h; } CGSize;

extern id objc_getClass(const char *);
extern SEL sel_registerName(const char *);
extern void objc_msgSend(void);
extern Class objc_allocateClassPair(Class, const char *, size_t);
extern bool class_addMethod(Class, SEL, void *, const char *);
extern void objc_registerClassPair(Class);
extern bool class_addProtocol(Class, Protocol *);
extern Protocol *objc_getProtocol(const char *);

typedef int pid_t;
typedef void *posix_spawn_file_actions_t;
extern int posix_spawn(pid_t *, const char *, const posix_spawn_file_actions_t *, const void *, char *const[], char *const[]);
extern int posix_spawn_file_actions_init(posix_spawn_file_actions_t *);
extern int posix_spawn_file_actions_adddup2(posix_spawn_file_actions_t *, int, int);
extern int posix_spawn_file_actions_addclose(posix_spawn_file_actions_t *, int);
extern int posix_spawn_file_actions_addinherit_np(posix_spawn_file_actions_t *, int);
typedef void *posix_spawnattr_t;
extern int posix_spawnattr_init(posix_spawnattr_t *);
extern int posix_spawnattr_setflags(posix_spawnattr_t *, short);
extern int fcntl(int, int, ...);
extern void (*signal(int, void (*)(int)))(int);
extern int midiStart(int toCore, int fromCore); // midi.c
extern int pipe(int[2]);
extern long read(int, void *, unsigned long);
extern int close(int);
extern int kill(pid_t, int);
extern int _NSGetExecutablePath(char *, uint32_t *);
extern char ***_NSGetEnviron(void);
extern unsigned long strlen(const char *);
extern void exit(int);
extern id NSHomeDirectory(void);

#define MSG(ret, ...) ((ret (*)(id, SEL, ##__VA_ARGS__))objc_msgSend)
#define CLS(n) objc_getClass(n)
#define SEL_(n) sel_registerName(n)

static id webView;
static id mainWindow;
static id statusItem;
static id statusMenu;
static pid_t corePid;

static id str(const char *s) { return MSG(id, const char *)(CLS("NSString"), SEL_("stringWithUTF8String:"), s); }
static const char *cstr(id s) { return s ? MSG(const char *)(s, SEL_("UTF8String")) : ""; }

static void js(const char *code) {
	MSG(void, id, id)(webView, SEL_("evaluateJavaScript:completionHandler:"), str(code), 0);
}

static void alertAndExit(const char *title, const char *text) {
	id a = MSG(id)(MSG(id)(CLS("NSAlert"), SEL_("alloc")), SEL_("init"));
	MSG(void, id)(a, SEL_("setMessageText:"), str(title));
	MSG(void, id)(a, SEL_("setInformativeText:"), str(text));
	MSG(long)(a, SEL_("runModal"));
	exit(1);
}

// ---- menu commands -> web UI ----
static const char *commands[] = {"about", "open", "save", "hardware", "channels", "settings", "pollNow", "togglePolling", "undo", "redo", "newSession", "clearRecent"};

static void menuAction(id self, SEL _cmd, id sender) {
	long tag = MSG(long)(sender, SEL_("tag"));
	if (tag < 0 || tag >= (long)(sizeof commands / sizeof *commands)) return;
	char buf[96] = "window.app && app.menu('";
	unsigned long n = strlen(buf);
	const char *c = commands[tag];
	while (*c && n < sizeof buf - 4) buf[n++] = *c++;
	buf[n++] = '\''; buf[n++] = ')'; buf[n] = 0;
	js(buf);
}

// ---- window and menu bar icon ----
enum { POLICY_REGULAR = 0 };

static double now(void) { return MSG(double)(CLS("NSDate"), SEL_("timeIntervalSinceReferenceDate")); }
static double quietUntil; // after a start in the background: ignore "reopen" until then

static void showWindow(void) {
	id app = MSG(id)(CLS("NSApplication"), SEL_("sharedApplication"));
	MSG(void, id)(mainWindow, SEL_("makeKeyAndOrderFront:"), 0);
	MSG(void, bool)(app, SEL_("activateIgnoringOtherApps:"), true);
}

static bool shouldTerminateAfterLastWindow(id self, SEL _cmd, id app) { return false; }

// Dock icon / Finder: show the window. Plug-ins that start the app send the
// same event again when several instances open at once; right after a start
// in the background it is ignored, so the app stays in the menu bar.
static bool shouldHandleReopen(id self, SEL _cmd, id app, bool visible) {
	if (now() < quietUntil) return false;
	showWindow();
	return true;
}

// switching to the app (Cmd-Tab, Dock) brings its window back; not in the
// first seconds after a start in the background (macOS may activate it then)
static double activeQuietUntil;
static void didBecomeActive(id self, SEL _cmd, id note) {
	if (now() < activeQuietUntil) return;
	if (mainWindow && !MSG(bool)(mainWindow, SEL_("isVisible"))) showWindow();
}

// A plug-in that starts the app leaves the file "launching" next to the
// settings just before. If it is fresh, the app starts in the background even
// when the --background argument did not come through.
static bool startedByPlugin(void) {
	id fm = MSG(id)(CLS("NSFileManager"), SEL_("defaultManager"));
	id home = NSHomeDirectory(); // (homeDirectoryForCurrentUser needs macOS 10.12)
	if (!home) return false;
	id path = MSG(id, id)(home, SEL_("stringByAppendingPathComponent:"), str("Library/Application Support/21 Remote/launching"));
	id attrs = MSG(id, id, id)(fm, SEL_("attributesOfItemAtPath:error:"), path, 0);
	if (!attrs) return false;
	id date = MSG(id)(attrs, SEL_("fileModificationDate"));
	double age = date ? -MSG(double)(date, SEL_("timeIntervalSinceNow")) : 1e9;
	MSG(bool, id, id)(fm, SEL_("removeItemAtPath:error:"), path, 0);
	return age >= 0 && age < 60;
}

static void statusOpen(id self, SEL _cmd, id sender) { showWindow(); }
static void statusSettings(id self, SEL _cmd, id sender) {
	showWindow();
	js("window.app && app.menu('settings')");
}

static void statusClick(id self, SEL _cmd, id sender) {
	id app = MSG(id)(CLS("NSApplication"), SEL_("sharedApplication"));
	id ev = MSG(id)(app, SEL_("currentEvent"));
	unsigned long type = ev ? MSG(unsigned long)(ev, SEL_("type")) : 1;
	unsigned long mods = ev ? MSG(unsigned long)(ev, SEL_("modifierFlags")) : 0;
	if (type == 4 /* right mouse up */ || type == 3 || (mods & (1UL << 18) /* control */)) {
		// attach the menu just for this click so a left click still opens the window
		id button = MSG(id)(statusItem, SEL_("button"));
		MSG(void, id)(statusItem, SEL_("setMenu:"), statusMenu);
		MSG(void, id)(button, SEL_("performClick:"), 0);
		MSG(void, id)(statusItem, SEL_("setMenu:"), 0);
		return;
	}
	showWindow();
}

static void willTerminate(id self, SEL _cmd, id note) {
	if (corePid > 0) kill(corePid, 15);
}

// ---- native open/save panels requested by the page ----
static void reply(long reqId, const char *path) {
	char buf[8192];
	unsigned long n = 0;
	const char *pre = "window.__nativeReply && __nativeReply(";
	while (*pre) buf[n++] = *pre++;
	char num[24]; int k = 0;
	long v = reqId < 0 ? 0 : reqId;
	do { num[k++] = (char)('0' + v % 10); v /= 10; } while (v && k < 20);
	while (k) buf[n++] = num[--k];
	buf[n++] = ','; buf[n++] = '"';
	for (const char *p = path; *p && n < sizeof buf - 8; p++) {
		unsigned char ch = (unsigned char)*p;
		if (ch == '"' || ch == '\\') { buf[n++] = '\\'; buf[n++] = (char)ch; }
		else if (ch < 0x20) { continue; }
		else buf[n++] = (char)ch;
	}
	buf[n++] = '"'; buf[n++] = ')'; buf[n] = 0;
	js(buf);
}

// ---- File > Open Recent (the page sends the list of recent sessions) ----
static id recentMenu;

static void recentAction(id self, SEL _cmd, id sender) {
	long i = MSG(long)(sender, SEL_("tag")) - 1000;
	char buf[64] = "window.app && app.openRecent(";
	unsigned long n = strlen(buf);
	char num[8]; int k = 0;
	do { num[k++] = (char)('0' + i % 10); i /= 10; } while (i > 0 && k < 6);
	while (k) buf[n++] = num[--k];
	buf[n++] = ')'; buf[n] = 0;
	js(buf);
}

static id addItem(id menu, const char *title, const char *action, const char *key, long mods, long tag);
static void sep(id menu);
static id delegate;

static void fillRecent(id names) {
	if (!recentMenu) return;
	MSG(void)(recentMenu, SEL_("removeAllItems"));
	unsigned long n = names ? MSG(unsigned long)(names, SEL_("count")) : 0;
	for (unsigned long i = 0; i < n && i < 20; i++) {
		id name = MSG(id, unsigned long)(names, SEL_("objectAtIndex:"), i);
		id it = addItem(recentMenu, cstr(name), "recentAction:", "", -1, -1);
		MSG(void, long)(it, SEL_("setTag:"), (long)(1000 + i));
		MSG(void, id)(it, SEL_("setTarget:"), delegate);
	}
	if (n == 0) {
		id it = addItem(recentMenu, "No Recent Sessions", 0, "", -1, -1);
		MSG(void, bool)(it, SEL_("setEnabled:"), false);
	} else {
		sep(recentMenu);
		addItem(recentMenu, "Clear Menu", "menuAction:", "", -1, 11);
	}
}

static void didReceiveMessage(id self, SEL _cmd, id controller, id message) {
	id body = MSG(id)(message, SEL_("body"));
	id cmd = MSG(id, id)(body, SEL_("objectForKey:"), str("cmd"));
	if (MSG(bool, id)(cmd, SEL_("isEqualToString:"), str("recent"))) {
		fillRecent(MSG(id, id)(body, SEL_("objectForKey:"), str("names")));
		return;
	}
	id rid = MSG(id, id)(body, SEL_("objectForKey:"), str("id"));
	long reqId = rid ? MSG(long)(rid, SEL_("longValue")) : 0;
	const char *c = cstr(cmd);
	bool save = c[0] == 's';
	id panel = MSG(id)(CLS(save ? "NSSavePanel" : "NSOpenPanel"), SEL_(save ? "savePanel" : "openPanel"));
	id types = MSG(id, id)(CLS("NSArray"), SEL_("arrayWithObject:"), str("xml"));
	MSG(void, id)(panel, SEL_("setAllowedFileTypes:"), types);
	MSG(void, id)(panel, SEL_("setTitle:"), str(save ? "Save Session" : "Open Session"));
	if (save) {
		id name = MSG(id, id)(body, SEL_("objectForKey:"), str("name"));
		MSG(void, id)(panel, SEL_("setNameFieldStringValue:"), name ? name : str("Session.xml"));
	} else {
		MSG(void, bool)(panel, SEL_("setCanChooseDirectories:"), false);
		MSG(void, bool)(panel, SEL_("setAllowsMultipleSelection:"), false);
	}
	long r = MSG(long)(panel, SEL_("runModal"));
	const char *path = "";
	if (r == 1) path = cstr(MSG(id)(MSG(id)(panel, SEL_("URL")), SEL_("path")));
	reply(reqId, path);
}

// ---- menu building ----
enum { CMD = 1 << 20, SHIFT = 1 << 17, OPT = 1 << 19, CTRL = 1 << 18 };

static id addItem(id menu, const char *title, const char *action, const char *key, long mods, long tag) {
	id item = MSG(id)(CLS("NSMenuItem"), SEL_("alloc"));
	item = MSG(id, id, SEL, id)(item, SEL_("initWithTitle:action:keyEquivalent:"), str(title), action ? SEL_(action) : 0, str(key));
	if (mods >= 0) MSG(void, unsigned long)(item, SEL_("setKeyEquivalentModifierMask:"), (unsigned long)mods);
	if (tag >= 0) {
		MSG(void, long)(item, SEL_("setTag:"), tag);
		MSG(void, id)(item, SEL_("setTarget:"), delegate);
	}
	MSG(void, id)(menu, SEL_("addItem:"), item);
	return item;
}

static void sep(id menu) { MSG(void, id)(menu, SEL_("addItem:"), MSG(id)(CLS("NSMenuItem"), SEL_("separatorItem"))); }

// ---- Window > Scale: page zoom of the web view; the window grows or shrinks with it ----
#if defined(__x86_64__)
extern void objc_msgSend_stret(void); // structs larger than 16 bytes come back through memory on x86_64
#define RECT_SEND objc_msgSend_stret
#else
#define RECT_SEND objc_msgSend
#endif
#define RECT0 ((CGRect (*)(id, SEL))RECT_SEND)
#define RECT1 ((CGRect (*)(id, SEL, CGRect))RECT_SEND)

static const double scales[] = {0.5, 0.75, 1.0, 1.25};
enum { NSCALES = 4, TAG_SCALE = 2000, TAG_ZOOMIN = 2010, TAG_ZOOMOUT = 2011, TAG_ACTUAL = 2012 };
static id scaleItems[NSCALES];
static double scale = 1.0;

// Before macOS 11 the page is scaled with CSS zoom, set again on every load.
static id contentController;

static void cssZoom(double s) {
	const char *v = s == 0.5 ? "0.5" : s == 0.75 ? "0.75" : s == 1.25 ? "1.25" : "1";
	char code[96] = "document.documentElement.style.zoom='";
	unsigned long n = strlen(code);
	while (*v) code[n++] = *v++;
	code[n++] = '\''; code[n] = 0;
	if (contentController) {
		MSG(void)(contentController, SEL_("removeAllUserScripts"));
		id us = MSG(id)(CLS("WKUserScript"), SEL_("alloc"));
		us = MSG(id, id, long, bool)(us, SEL_("initWithSource:injectionTime:forMainFrameOnly:"), str(code), 1 /* document end */, true);
		MSG(void, id)(contentController, SEL_("addUserScript:"), us);
	}
	js(code);
}

static void applyScale(double s, bool resize) {
	double old = scale;
	scale = s;
	if (MSG(bool, SEL)(webView, SEL_("respondsToSelector:"), SEL_("setPageZoom:")))
		MSG(void, double)(webView, SEL_("setPageZoom:"), s);
	else
		cssZoom(s); // pageZoom needs macOS 11
	for (int i = 0; i < NSCALES; i++)
		if (scaleItems[i]) MSG(void, long)(scaleItems[i], SEL_("setState:"), scales[i] == s ? 1 : 0);
	double minW = 760 * s, minH = 520 * s;
	MSG(void, CGSize)(mainWindow, SEL_("setContentMinSize:"), (CGSize){minW, minH});
	id defs = MSG(id)(CLS("NSUserDefaults"), SEL_("standardUserDefaults"));
	MSG(void, double, id)(defs, SEL_("setDouble:forKey:"), s, str("Scale"));
	if (!resize || old == s) return;
	CGRect fr = RECT0(mainWindow, SEL_("frame"));
	CGRect cr = RECT1(mainWindow, SEL_("contentRectForFrameRect:"), fr);
	cr.w *= s / old;
	cr.h *= s / old;
	if (cr.w < minW) cr.w = minW;
	if (cr.h < minH) cr.h = minH;
	CGRect nf = RECT1(mainWindow, SEL_("frameRectForContentRect:"), cr);
	nf.x = fr.x;
	nf.y = fr.y + fr.h - nf.h; // keep the top left corner in place
	id screen = MSG(id)(mainWindow, SEL_("screen"));
	if (screen) {
		CGRect vf = RECT0(screen, SEL_("visibleFrame"));
		if (nf.w > vf.w) nf.w = vf.w;
		if (nf.h > vf.h) { nf.h = vf.h; nf.y = fr.y + fr.h - nf.h; }
		if (nf.x + nf.w > vf.x + vf.w) nf.x = vf.x + vf.w - nf.w;
		if (nf.x < vf.x) nf.x = vf.x;
		if (nf.y + nf.h > vf.y + vf.h) nf.y = vf.y + vf.h - nf.h;
		if (nf.y < vf.y) nf.y = vf.y;
	}
	MSG(void, CGRect, bool, bool)(mainWindow, SEL_("setFrame:display:animate:"), nf, true, true);
}

static void scaleAction(id self, SEL _cmd, id sender) {
	long tag = MSG(long)(sender, SEL_("tag"));
	int cur = 2;
	for (int i = 0; i < NSCALES; i++)
		if (scales[i] == scale) cur = i;
	if (tag >= TAG_SCALE && tag < TAG_SCALE + NSCALES) cur = (int)(tag - TAG_SCALE);
	else if (tag == TAG_ZOOMIN && cur < NSCALES - 1) cur++;
	else if (tag == TAG_ZOOMOUT && cur > 0) cur--;
	else if (tag == TAG_ACTUAL) cur = 2;
	applyScale(scales[cur], true);
}

static id scaleItem(id menu, const char *title, const char *key, long mods, long tag) {
	id it = addItem(menu, title, "scaleAction:", key, mods, -1);
	MSG(void, long)(it, SEL_("setTag:"), tag);
	MSG(void, id)(it, SEL_("setTarget:"), delegate);
	return it;
}


static id submenu(id bar, const char *title) {
	id m = MSG(id, id)(MSG(id)(CLS("NSMenu"), SEL_("alloc")), SEL_("initWithTitle:"), str(title));
	id it = addItem(bar, title, 0, "", -1, -1);
	MSG(void, id)(it, SEL_("setSubmenu:"), m);
	return m;
}

static void buildMenu(id app) {
	id bar = MSG(id)(MSG(id)(CLS("NSMenu"), SEL_("alloc")), SEL_("init"));
	id appm = submenu(bar, "21 Remote");
	addItem(appm, "About 21 Remote", "menuAction:", "", -1, 0);
	sep(appm);
	addItem(appm, "Hide 21 Remote", "hide:", "h", CMD, -1);
	addItem(appm, "Hide Others", "hideOtherApplications:", "h", CMD | OPT, -1);
	addItem(appm, "Show All", "unhideAllApplications:", "", -1, -1);
	sep(appm);
	addItem(appm, "Quit 21 Remote", "terminate:", "q", CMD, -1);

	id file = submenu(bar, "File");
	addItem(file, "New Session", "menuAction:", "n", CMD, 10);
	addItem(file, "Open Session…", "menuAction:", "o", CMD, 1);
	id recentItem = addItem(file, "Open Recent", 0, "", -1, -1);
	recentMenu = MSG(id, id)(MSG(id)(CLS("NSMenu"), SEL_("alloc")), SEL_("initWithTitle:"), str("Open Recent"));
	MSG(void, bool)(recentMenu, SEL_("setAutoenablesItems:"), false);
	MSG(void, id)(recentItem, SEL_("setSubmenu:"), recentMenu);
	fillRecent(0);
	sep(file);
	addItem(file, "Save Session…", "menuAction:", "s", CMD, 2);
	sep(file);
	addItem(file, "Close Window", "performClose:", "w", CMD, -1);

	id edit = submenu(bar, "Edit");
	// the page decides: text being edited, or the last change to the rack
	addItem(edit, "Undo", "menuAction:", "z", CMD, 8);
	addItem(edit, "Redo", "menuAction:", "z", CMD | SHIFT, 9);
	sep(edit);
	addItem(edit, "Cut", "cut:", "x", CMD, -1);
	addItem(edit, "Copy", "copy:", "c", CMD, -1);
	addItem(edit, "Paste", "paste:", "v", CMD, -1);
	addItem(edit, "Select All", "selectAll:", "a", CMD, -1);

	id view = submenu(bar, "View");
	addItem(view, "Rack", "menuAction:", "1", CMD, 3);
	addItem(view, "Channels", "menuAction:", "2", CMD, 4);
	addItem(view, "Settings", "menuAction:", "3", CMD, 5);
	sep(view);
	addItem(view, "Enter Full Screen", "toggleFullScreen:", "f", CMD | CTRL, -1);

	id poll = submenu(bar, "Polling");
	addItem(poll, "Poll Now", "menuAction:", "r", CMD, 6);
	addItem(poll, "Start or Stop Polling", "menuAction:", "p", CMD | SHIFT, 7);

	id win = submenu(bar, "Window");
	addItem(win, "Minimize", "performMiniaturize:", "m", CMD, -1);
	addItem(win, "Zoom", "performZoom:", "", -1, -1);
	sep(win);
	scaleItem(win, "Actual Size", "0", CMD, TAG_ACTUAL);
	scaleItem(win, "Zoom In", "+", CMD, TAG_ZOOMIN);
	scaleItem(win, "Zoom Out", "-", CMD, TAG_ZOOMOUT);
	id scaleMenuItem = addItem(win, "Scale", 0, "", -1, -1);
	id scaleMenu = MSG(id, id)(MSG(id)(CLS("NSMenu"), SEL_("alloc")), SEL_("initWithTitle:"), str("Scale"));
	MSG(void, id)(scaleMenuItem, SEL_("setSubmenu:"), scaleMenu);
	const char *names[NSCALES] = {"50 %", "75 %", "100 %", "125 %"};
	for (int i = 0; i < NSCALES; i++) scaleItems[i] = scaleItem(scaleMenu, names[i], "", -1, TAG_SCALE + i);
	sep(win);
	MSG(void, id)(app, SEL_("setWindowsMenu:"), win);

	MSG(void, id)(app, SEL_("setMainMenu:"), bar);
}

static void buildStatusItem(void) {
	id bar = MSG(id)(CLS("NSStatusBar"), SEL_("systemStatusBar"));
	statusItem = MSG(id, double)(bar, SEL_("statusItemWithLength:"), -1.0 /* variable */);
	MSG(id)(statusItem, SEL_("retain"));
	id button = MSG(id)(statusItem, SEL_("button"));
	id bundle = MSG(id)(CLS("NSBundle"), SEL_("mainBundle"));
	id path = MSG(id, id, id)(bundle, SEL_("pathForResource:ofType:"), str("StatusIcon"), str("png"));
	id img = path ? MSG(id, id)(MSG(id)(CLS("NSImage"), SEL_("alloc")), SEL_("initWithContentsOfFile:"), path) : 0;
	if (img) {
		MSG(void, CGSize)(img, SEL_("setSize:"), (CGSize){18, 18});
		MSG(void, bool)(img, SEL_("setTemplate:"), true); // follows light/dark menu bar
		MSG(void, id)(button, SEL_("setImage:"), img);
	} else {
		MSG(void, id)(button, SEL_("setTitle:"), str("21"));
	}
	MSG(void, id)(button, SEL_("setToolTip:"), str("21 Remote is running"));
	MSG(void, id)(button, SEL_("setTarget:"), delegate);
	MSG(void, SEL)(button, SEL_("setAction:"), SEL_("statusClick:"));
	MSG(long, unsigned long)(button, SEL_("sendActionOn:"), (1UL << 2) | (1UL << 4)); // left and right mouse up

	statusMenu = MSG(id, id)(MSG(id)(CLS("NSMenu"), SEL_("alloc")), SEL_("initWithTitle:"), str("21 Remote"));
	id it = addItem(statusMenu, "Open", "statusOpen:", "", -1, -1);
	MSG(void, id)(it, SEL_("setTarget:"), delegate);
	it = addItem(statusMenu, "Settings", "statusSettings:", "", -1, -1);
	MSG(void, id)(it, SEL_("setTarget:"), delegate);
	sep(statusMenu);
	addItem(statusMenu, "Quit", "terminate:", "", -1, -1);
}

// ---- engine start-up ----
static void startCore(char *url, unsigned long urlSize) {
	char exe[4096];
	uint32_t sz = sizeof exe;
	if (_NSGetExecutablePath(exe, &sz) != 0) alertAndExit("21 Remote", "Cannot locate the application.");
	unsigned long n = strlen(exe);
	while (n && exe[n - 1] != '/') n--;
	const char *name = "remote21-core";
	for (const char *p = name; *p && n < sizeof exe - 1; p++) exe[n++] = *p;
	exe[n] = 0;

	// Pipes: in = our end keeps the engine alive (its stdin), out = engine's
	// first line (URL), mi/mo = CoreMIDI bridge (engine fds 3 and 4).
	int out[2], in[2], mi[2], mo[2];
	if (pipe(out) || pipe(in) || pipe(mi) || pipe(mo)) alertAndExit("21 Remote", "Cannot create pipes.");
	int *all[] = {&out[0], &out[1], &in[0], &in[1], &mi[0], &mi[1], &mo[0], &mo[1]};
	for (int i = 0; i < 8; i++) { // move away from 0-4 so the dup2 targets never collide
		int hi = fcntl(*all[i], 0 /* F_DUPFD */, 20);
		close(*all[i]);
		*all[i] = hi;
		fcntl(hi, 2 /* F_SETFD */, 1 /* FD_CLOEXEC */);
	}
	posix_spawn_file_actions_t fa;
	posix_spawn_file_actions_init(&fa);
	posix_spawn_file_actions_adddup2(&fa, in[0], 0);
	posix_spawn_file_actions_adddup2(&fa, out[1], 1);
	posix_spawn_file_actions_addinherit_np(&fa, 2);
	posix_spawn_file_actions_adddup2(&fa, mi[0], 3);
	posix_spawn_file_actions_adddup2(&fa, mo[1], 4);
	posix_spawnattr_t attr;
	posix_spawnattr_init(&attr);
	posix_spawnattr_setflags(&attr, 0x4000 /* POSIX_SPAWN_CLOEXEC_DEFAULT: only the fds above are inherited */);
	char *argv[] = {exe, "--embedded", "--midi-fds", 0};
	if (posix_spawn(&corePid, exe, &fa, &attr, argv, *_NSGetEnviron()) != 0)
		alertAndExit("21 Remote could not start", "The engine (remote21-core) is missing or not executable. Reinstall the application.");
	close(in[0]);
	close(out[1]);
	close(mi[0]);
	close(mo[1]);
	// in[1] stays open for our lifetime: the engine exits when it closes.
	midiStart(mi[1], mo[0]);

	char line[1024];
	unsigned long len = 0;
	while (len < sizeof line - 1) {
		char ch;
		long r = read(out[0], &ch, 1);
		if (r <= 0) break;
		if (ch == '\n') break;
		line[len++] = ch;
	}
	line[len] = 0;
	close(out[0]);
	const char *tag = "R21-URL ";
	unsigned long t = 0;
	while (tag[t] && line[t] == tag[t]) t++;
	if (tag[t] != 0 || len - t >= urlSize)
		alertAndExit("21 Remote could not start", "The engine did not start. See ~/Library/Logs/21 Remote.log for details.");
	unsigned long i = 0;
	for (; line[t + i]; i++) url[i] = line[t + i];
	url[i] = 0;
}

static bool sameStr(const char *a, const char *b) {
	while (*a && *a == *b) { a++; b++; }
	return *a == *b;
}

int main(int argc, char **argv) {
	signal(13 /* SIGPIPE */, (void (*)(int))1 /* SIG_IGN */);
	id pool = MSG(id)(MSG(id)(CLS("NSAutoreleasePool"), SEL_("alloc")), SEL_("init"));
	(void)pool;
	bool background = false;
	for (int i = 1; i < argc; i++)
		if (sameStr(argv[i], "--background")) background = true;
	if (startedByPlugin()) background = true;
	id app = MSG(id)(CLS("NSApplication"), SEL_("sharedApplication"));
	MSG(bool, long)(app, SEL_("setActivationPolicy:"), POLICY_REGULAR);
	if (background) {
		quietUntil = now() + 20;
		activeQuietUntil = now() + 5;
	}

	char url[900];
	startCore(url, sizeof url);

	Class dc = objc_allocateClassPair(CLS("NSObject"), "Remote21ShellDelegate", 0);
	class_addMethod(dc, SEL_("menuAction:"), (void *)menuAction, "v@:@");
	class_addMethod(dc, SEL_("applicationShouldTerminateAfterLastWindowClosed:"), (void *)shouldTerminateAfterLastWindow, "B@:@");
	class_addMethod(dc, SEL_("applicationWillTerminate:"), (void *)willTerminate, "v@:@");
	class_addMethod(dc, SEL_("userContentController:didReceiveScriptMessage:"), (void *)didReceiveMessage, "v@:@@");
	class_addMethod(dc, SEL_("applicationShouldHandleReopen:hasVisibleWindows:"), (void *)shouldHandleReopen, "B@:@B");
	class_addMethod(dc, SEL_("applicationDidBecomeActive:"), (void *)didBecomeActive, "v@:@");
	class_addMethod(dc, SEL_("statusClick:"), (void *)statusClick, "v@:@");
	class_addMethod(dc, SEL_("recentAction:"), (void *)recentAction, "v@:@");
	class_addMethod(dc, SEL_("scaleAction:"), (void *)scaleAction, "v@:@");
	class_addMethod(dc, SEL_("statusOpen:"), (void *)statusOpen, "v@:@");
	class_addMethod(dc, SEL_("statusSettings:"), (void *)statusSettings, "v@:@");
	Protocol *p1 = objc_getProtocol("NSApplicationDelegate");
	if (p1) class_addProtocol(dc, p1);
	Protocol *p2 = objc_getProtocol("WKScriptMessageHandler");
	if (p2) class_addProtocol(dc, p2);
	Protocol *p3 = objc_getProtocol("NSWindowDelegate");
	if (p3) class_addProtocol(dc, p3);
	objc_registerClassPair(dc);
	delegate = MSG(id)(MSG(id)(dc, SEL_("alloc")), SEL_("init"));
	MSG(void, id)(app, SEL_("setDelegate:"), delegate);
	buildMenu(app);
	buildStatusItem();

	CGRect frame = {0, 0, 1320, 820};
	id win = MSG(id)(CLS("NSWindow"), SEL_("alloc"));
	win = MSG(id, CGRect, unsigned long, unsigned long, bool)(win, SEL_("initWithContentRect:styleMask:backing:defer:"), frame, 1 | 2 | 4 | 8, 2, false);
	MSG(void, id)(win, SEL_("setTitle:"), str("21 Remote"));
	MSG(void, CGSize)(win, SEL_("setContentMinSize:"), (CGSize){760, 520});
	MSG(void, bool)(win, SEL_("setReleasedWhenClosed:"), false);
	MSG(void)(win, SEL_("center"));
	MSG(bool, id)(win, SEL_("setFrameAutosaveName:"), str("Remote21Main"));
	MSG(void, id)(win, SEL_("setDelegate:"), delegate);
	MSG(void, bool)(win, SEL_("setRestorable:"), false); // never reopened by window restoration
	mainWindow = win;

	id cfg = MSG(id)(MSG(id)(CLS("WKWebViewConfiguration"), SEL_("alloc")), SEL_("init"));
	id ucc = MSG(id)(cfg, SEL_("userContentController"));
	contentController = ucc;
	MSG(void, id, id)(ucc, SEL_("addScriptMessageHandler:name:"), delegate, str("native"));
	webView = MSG(id, CGRect, id)(MSG(id)(CLS("WKWebView"), SEL_("alloc")), SEL_("initWithFrame:configuration:"), frame, cfg);
	MSG(void, unsigned long)(webView, SEL_("setAutoresizingMask:"), 2 | 16);
	MSG(void, id)(win, SEL_("setContentView:"), webView);
	{
		id defs = MSG(id)(CLS("NSUserDefaults"), SEL_("standardUserDefaults"));
		double saved = MSG(double, id)(defs, SEL_("doubleForKey:"), str("Scale"));
		double s = 1.0;
		for (int i = 0; i < NSCALES; i++)
			if (saved == scales[i]) s = saved;
		applyScale(s, false);
	}

	id nsurl = MSG(id, id)(CLS("NSURL"), SEL_("URLWithString:"), str(url));
	id req = MSG(id, id)(CLS("NSURLRequest"), SEL_("requestWithURL:"), nsurl);
	MSG(id, id)(webView, SEL_("loadRequest:"), req);

	if (!background) {
		MSG(void, id)(win, SEL_("makeKeyAndOrderFront:"), 0);
		MSG(void, bool)(app, SEL_("activateIgnoringOtherApps:"), true);
	}
	MSG(void)(app, SEL_("run"));
	return 0;
}
