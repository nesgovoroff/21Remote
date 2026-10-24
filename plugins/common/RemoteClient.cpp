// SPDX-License-Identifier: AGPL-3.0-or-later
#include "RemoteClient.h"
#include <signal.h>
#include <string>

namespace remote21
{

juce::File discoveryFile()
{
    return juce::File::getSpecialLocation (juce::File::userApplicationDataDirectory)
        .getChildFile ("Application Support")
        .getChildFile ("21 Remote")
        .getChildFile ("plugin.json");
}

static juce::Colour parseColour (const juce::String& hex)
{
    auto h = hex.trimCharactersAtStart ("#");
    if (h.length() != 6)
        return juce::Colour (0xff646464);
    return juce::Colour ((juce::uint32) (0xff000000u | (juce::uint32) h.getHexValue32()));
}

Client::Client (juce::String name) : juce::Thread ("21 Remote client"), clientName (std::move (name))
{
    startThread();
}

Client::~Client()
{
    signalThreadShouldExit();
    wake.signal();
    {
        const juce::ScopedLock sl (lock);
        if (socket != nullptr)
            socket->close();
    }
    stopThread (3000);
}

juce::String Client::getStatus() const { const juce::ScopedLock sl (lock); return status; }
juce::String Client::getUiUrl() const { const juce::ScopedLock sl (lock); return uiUrl; }
bool Client::isRackOnline() const { const juce::ScopedLock sl (lock); return rackOnline; }
juce::String Client::getRackName() const { const juce::ScopedLock sl (lock); return rackName; }
std::vector<Channel> Client::getChannels() const { const juce::ScopedLock sl (lock); return channels; }

std::vector<Param> Client::getParams() const
{
    const juce::ScopedLock sl (lock);
    auto out = params;
    for (auto& p : out)
    {
        auto it = values.find (p.id);
        if (it != values.end())
            p.value = it->second;
    }
    return out;
}

bool Client::getParam (const juce::String& id, Param& out) const
{
    const juce::ScopedLock sl (lock);
    for (auto& p : params)
    {
        if (p.id == id)
        {
            out = p;
            auto it = values.find (id);
            if (it != values.end())
                out.value = it->second;
            return true;
        }
    }
    return false;
}

int Client::getValue (const juce::String& id, int fallback) const
{
    const juce::ScopedLock sl (lock);
    auto it = values.find (id);
    return it != values.end() ? it->second : fallback;
}

bool Client::findChannel (const juce::String& id, Channel& out) const
{
    const juce::ScopedLock sl (lock);
    for (auto& c : channels)
        if (c.id == id) { out = c; return true; }
    return false;
}

void Client::set (const juce::String& paramId, int value)
{
    {
        const juce::ScopedLock sl (lock);
        pending[paramId] = value;
        values[paramId] = value;
        localChange[paramId] = juce::Time::getMillisecondCounterHiRes() * 0.001;
    }
    wake.signal();
}

void Client::setTrack (const juce::String& stripId, const juce::String& name)
{
    {
        const juce::ScopedLock sl (lock);
        if (stripId == trackStrip && name == trackName)
            return;
        trackStrip = stripId;
        trackName = name;
        trackDirty = true;
    }
    wake.signal();
}

void Client::setComment (const juce::String& stripId, const juce::String& text)
{
    auto* c = new juce::DynamicObject();
    c->setProperty ("strip", stripId);
    c->setProperty ("text", text);
    auto* msg = new juce::DynamicObject();
    msg->setProperty ("comment", juce::var (c));
    {
        const juce::ScopedLock sl (lock);
        outbox.push_back (juce::JSON::toString (juce::var (msg), true));
        for (auto& ch : channels) // show it right away
            if (ch.id == stripId)
                ch.comment = text;
    }
    wake.signal();
    sendChangeMessage();
}

double Client::secondsSinceLocalChange (const juce::String& id) const
{
    const juce::ScopedLock sl (lock);
    auto it = localChange.find (id);
    if (it == localChange.end())
        return 1.0e9;
    return juce::Time::getMillisecondCounterHiRes() * 0.001 - it->second;
}

void Client::setStatus (const juce::String& s, bool isConnected)
{
    bool changed;
    {
        const juce::ScopedLock sl (lock);
        changed = (status != s) || (connected.load() != isConnected);
        status = s;
    }
    connected = isConnected;
    if (changed)
        sendChangeMessage();
}

// Several plug-in instances (also in other DAW processes) may want to start the
// app at the same moment. Only one start per 20 s, shared through a marker
// file, and none while the app's process is alive: asking a running app to
// open again would show its window.
static bool appStartedRecently()
{
    auto info = juce::JSON::parse (discoveryFile().loadFileAsString());
    int pid = (int) info.getProperty ("pid", 0);
    if (pid > 0 && ::kill (pid, 0) == 0)
        return true;
    auto marker = discoveryFile().getSiblingFile ("launching");
    auto age = juce::Time::getCurrentTime() - marker.getLastModificationTime();
    if (marker.existsAsFile() && age.inSeconds() < 20.0)
        return true;
    marker.getParentDirectory().createDirectory();
    marker.replaceWithText (juce::String (juce::Time::currentTimeMillis()));
    return false;
}

void Client::launchApp()
{
    auto now = juce::Time::getMillisecondCounterHiRes() * 0.001;
    if (now - lastLaunch < 20.0)
        return;
    lastLaunch = now;
    if (appStartedRecently())
        return;
    juce::ChildProcess p;
    // -g: keep the DAW in front; --background: only the menu bar icon, no window
    if (! p.start (juce::StringArray { "/usr/bin/open", "-g", "-b", "local.remote21.app", "--args", "--background" }))
        p.start (juce::StringArray { "/usr/bin/open", "-g", "-a", "21 Remote", "--args", "--background" });
    p.waitForProcessToFinish (5000);
}

bool Client::connectOnce()
{
    auto f = discoveryFile();
    auto info = juce::JSON::parse (f.loadFileAsString());
    int port = (int) info.getProperty ("port", 0);
    juce::String token = info.getProperty ("token", "").toString();
    if (port <= 0 || token.isEmpty())
    {
        setStatus ("Starting 21 Remote...", false);
        launchApp();
        return false;
    }
    auto s = std::make_unique<juce::StreamingSocket>();
    if (! s->connect ("127.0.0.1", port, 1500))
    {
        setStatus ("Starting 21 Remote...", false);
        launchApp();
        return false;
    }
    auto* hello = new juce::DynamicObject();
    hello->setProperty ("token", token);
    hello->setProperty ("client", clientName);
    auto* msg = new juce::DynamicObject();
    msg->setProperty ("hello", juce::var (hello));
    auto line = juce::JSON::toString (juce::var (msg), true) + "\n";
    if (s->write (line.toRawUTF8(), (int) line.getNumBytesAsUTF8()) <= 0)
        return false;
    const juce::ScopedLock sl (lock);
    socket = std::move (s);
    trackDirty = trackStrip.isNotEmpty() || trackName.isNotEmpty(); // the app forgot us
    return true;
}

void Client::handleLine (const juce::String& line)
{
    auto v = juce::JSON::parse (line);
    if (! v.isObject())
        return;
    bool notify = false;

    if (auto h = v.getProperty ("hello", {}); h.isObject())
    {
        {
            const juce::ScopedLock sl (lock);
            uiUrl = h.getProperty ("url", "").toString();
        }
        setStatus ("Connected", true);
        notify = true;
    }

    if (auto l = v.getProperty ("layout", {}); l.isObject())
    {
        std::vector<Channel> chs;
        if (auto* arr = l.getProperty ("channels", {}).getArray())
            for (auto& c : *arr)
            {
                Channel ch;
                ch.id = c.getProperty ("id", "").toString();
                ch.kind = c.getProperty ("kind", "").toString();
                ch.name = c.getProperty ("name", "").toString();
                ch.label = c.getProperty ("label", "").toString();
                ch.ids = c.getProperty ("ids", "").toString();
                ch.comment = c.getProperty ("comment", "").toString();
                ch.colour = parseColour (c.getProperty ("color", "").toString());
                ch.slot = (int) c.getProperty ("slot", 0);
                ch.ch = (int) c.getProperty ("ch", 0);
                ch.linked = (bool) c.getProperty ("linked", false);
                chs.push_back (ch);
            }
        std::vector<Param> ps;
        std::map<juce::String, int> vals;
        if (auto* arr = l.getProperty ("params", {}).getArray())
            for (auto& p : *arr)
            {
                Param x;
                x.id = p.getProperty ("id", "").toString();
                x.label = p.getProperty ("label", "").toString();
                x.kind = p.getProperty ("kind", "").toString();
                x.min = (int) p.getProperty ("min", 0);
                x.max = (int) p.getProperty ("max", 1);
                x.value = (int) p.getProperty ("value", 0);
                x.readOnly = (bool) p.getProperty ("readOnly", false);
                vals[x.id] = x.value;
                ps.push_back (x);
            }
        const juce::ScopedLock sl (lock);
        channels = std::move (chs);
        params = std::move (ps);
        for (auto& [id, val] : vals)
            if (pending.find (id) == pending.end())
                values[id] = val;
        rackOnline = (bool) l.getProperty ("online", false);
        rackName = l.getProperty ("rack", "").toString();
        notify = true;
    }

    if (auto vals = v.getProperty ("values", {}); vals.isObject())
    {
        if (auto* obj = vals.getDynamicObject())
        {
            const juce::ScopedLock sl (lock);
            for (auto& nv : obj->getProperties())
            {
                auto id = nv.name.toString();
                if (pending.find (id) == pending.end())
                    values[id] = (int) nv.value;
            }
        }
        notify = true;
    }

    if (notify)
        sendChangeMessage();
}

void Client::run()
{
    std::string partial; // raw bytes: a UTF-8 character may be split between reads
    while (! threadShouldExit())
    {
        if (socket == nullptr)
        {
            if (! connectOnce())
            {
                wake.wait (1000);
                continue;
            }
            partial.clear();
        }

        // flush queued changes
        std::map<juce::String, int> out;
        std::vector<juce::String> lines;
        {
            const juce::ScopedLock sl (lock);
            out.swap (pending);
            lines.swap (outbox);
            if (trackDirty)
            {
                trackDirty = false;
                auto* t = new juce::DynamicObject();
                t->setProperty ("strip", trackStrip);
                t->setProperty ("name", trackName);
                auto* msg = new juce::DynamicObject();
                msg->setProperty ("track", juce::var (t));
                lines.push_back (juce::JSON::toString (juce::var (msg), true));
            }
        }
        bool failed = false;
        for (auto& l : lines)
        {
            auto line = l + "\n";
            if (socket->write (line.toRawUTF8(), (int) line.getNumBytesAsUTF8()) <= 0)
                failed = true;
        }
        for (auto& [id, val] : out)
        {
            auto* set = new juce::DynamicObject();
            set->setProperty ("id", id);
            set->setProperty ("value", val);
            auto* msg = new juce::DynamicObject();
            msg->setProperty ("set", juce::var (set));
            auto line = juce::JSON::toString (juce::var (msg), true) + "\n";
            if (socket->write (line.toRawUTF8(), (int) line.getNumBytesAsUTF8()) <= 0)
                failed = true;
        }

        // read whatever arrived (wait at most 30 ms so queued sets go out quickly)
        if (! failed)
        {
            int ready = socket->waitUntilReady (true, 30);
            if (ready < 0)
                failed = true;
            else if (ready > 0)
            {
                char data[16384];
                int n = socket->read (data, (int) sizeof (data), false);
                if (n <= 0)
                    failed = true;
                else
                {
                    partial.append (data, (size_t) n);
                    size_t nl;
                    while ((nl = partial.find ('\n')) != std::string::npos)
                    {
                        handleLine (juce::String::fromUTF8 (partial.data(), (int) nl));
                        partial.erase (0, nl + 1);
                    }
                    if (partial.size() > (1u << 22))
                        partial.clear(); // runaway line
                }
            }
        }

        if (failed)
        {
            {
                const juce::ScopedLock sl (lock);
                socket->close();
                socket.reset();
            }
            setStatus ("21 Remote is not running. Trying again...", false);
            wake.wait (1000);
        }
    }
}

} // namespace remote21
