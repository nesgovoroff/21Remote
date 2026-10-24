// SPDX-License-Identifier: AGPL-3.0-or-later
// Connection from a plug-in instance to the running 21 Remote app.
//
// The app owns the link to the rack; plug-ins connect to it over 127.0.0.1
// (port and token come from ~/Library/Application Support/21 Remote/plugin.json).
// If the app is not running, the client starts it in the background.
#pragma once

#include <juce_core/juce_core.h>
#include <juce_events/juce_events.h>
#include <juce_graphics/juce_graphics.h>
#include <map>

namespace remote21
{

struct Channel
{
    juce::String id, kind, name, label, ids, comment;
    juce::Colour colour;
    int slot = 0, ch = 0;
    bool linked = false;

    juce::String displayName() const { return label.isEmpty() ? name : name + "  " + label; }
};

struct Param
{
    juce::String id, label, kind; // kind: range, bool, enum
    int min = 0, max = 1, value = 0;
    bool readOnly = false;
};

// Listen with juce::ChangeListener; change messages arrive on the message thread
// whenever the connection state, the layout or any value changes.
class Client : public juce::ChangeBroadcaster, private juce::Thread
{
public:
    explicit Client (juce::String clientName);
    ~Client() override;

    bool isConnected() const { return connected.load(); }
    juce::String getStatus() const;
    juce::String getUiUrl() const;
    bool isRackOnline() const;
    juce::String getRackName() const;

    std::vector<Channel> getChannels() const;
    std::vector<Param> getParams() const;
    bool getParam (const juce::String& id, Param& out) const;
    int getValue (const juce::String& id, int fallback) const;
    bool findChannel (const juce::String& id, Channel& out) const;

    // queue a value change for the rack (any thread)
    void set (const juce::String& paramId, int value);

    // the DAW track this plug-in sits on and the rack channel it controls; the
    // app uses the name for "Take ch. names from DAW" (resent after reconnects)
    void setTrack (const juce::String& stripId, const juce::String& trackName);

    // comment of a rack channel; it is kept in the app, not in the plug-in
    void setComment (const juce::String& stripId, const juce::String& text);

    // seconds since this client last changed id (to ignore our own echoes)
    double secondsSinceLocalChange (const juce::String& id) const;

private:
    void run() override;
    bool connectOnce();
    void handleLine (const juce::String& line);
    void launchApp();
    void setStatus (const juce::String& s, bool isConnected);

    juce::String clientName;
    std::atomic<bool> connected { false };
    std::unique_ptr<juce::StreamingSocket> socket;

    mutable juce::CriticalSection lock;
    juce::String status { "Connecting to 21 Remote..." }, uiUrl, rackName;
    bool rackOnline = false;
    std::vector<Channel> channels;
    std::vector<Param> params;
    std::map<juce::String, int> values;
    std::map<juce::String, int> pending;
    std::map<juce::String, double> localChange;
    juce::String trackStrip, trackName;
    bool trackDirty = false;
    std::vector<juce::String> outbox; // JSON lines to send
    double lastLaunch = -1.0e9;
    juce::WaitableEvent wake;

    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR (Client)
};

juce::File discoveryFile();

} // namespace remote21
