// SPDX-License-Identifier: AGPL-3.0-or-later
#include "ChannelProcessor.h"
#include "ChannelEditor.h"

const char* const ChannelProcessor::paramIds[numControls] = { "gain", "phantom", "hpf", "opt1", "opt2" };

static juce::String dbText (float v, int)
{
    int d = juce::roundToInt (v);
    return (d > 0 ? "+" : "") + juce::String (d) + " dB";
}

juce::AudioProcessorValueTreeState::ParameterLayout ChannelProcessor::createLayout()
{
    juce::AudioProcessorValueTreeState::ParameterLayout l;
    l.add (std::make_unique<juce::AudioParameterFloat> (
        juce::ParameterID { "gain", 1 }, "Gain", juce::NormalisableRange<float> (-13.0f, 75.0f, 1.0f), 0.0f,
        juce::AudioParameterFloatAttributes().withLabel ("dB").withStringFromValueFunction (dbText)));
    // phantom power is deliberately not automatable: a stray automation point
    // must not switch 48 V while monitors are up
    l.add (std::make_unique<juce::AudioParameterBool> (
        juce::ParameterID { "phantom", 1 }, "48 V", false, juce::AudioParameterBoolAttributes().withAutomatable (false)));
    l.add (std::make_unique<juce::AudioParameterBool> (juce::ParameterID { "hpf", 1 }, "HPF", false));
    l.add (std::make_unique<juce::AudioParameterBool> (juce::ParameterID { "opt1", 1 }, "Insert / DC servo", false));
    l.add (std::make_unique<juce::AudioParameterBool> (juce::ParameterID { "opt2", 1 }, "Soft clip / CM servo", false));
    return l;
}

ChannelProcessor::ChannelProcessor()
    : juce::AudioProcessor (BusesProperties()
                                .withInput ("Input", juce::AudioChannelSet::stereo(), true)
                                .withOutput ("Output", juce::AudioChannelSet::stereo(), true)),
      apvts (*this, nullptr, "RemoteChannel", createLayout())
{
    rackClient = std::make_unique<remote21::Client> ("21 Channel");
    rackClient->addChangeListener (this);
    for (auto* id : paramIds)
        apvts.addParameterListener (id, this);
    startTimerHz (30);
}

ChannelProcessor::~ChannelProcessor()
{
    stopTimer();
    for (auto* id : paramIds)
        apvts.removeParameterListener (id, this);
    rackClient->removeChangeListener (this);
    rackClient.reset();
}

bool ChannelProcessor::isBusesLayoutSupported (const BusesLayout& layouts) const
{
    auto in = layouts.getMainInputChannelSet(), out = layouts.getMainOutputChannelSet();
    return in == out && (out == juce::AudioChannelSet::mono() || out == juce::AudioChannelSet::stereo());
}

void ChannelProcessor::processBlock (juce::AudioBuffer<float>& buffer, juce::MidiBuffer&)
{
    // pass-through: nothing to do with the audio
    for (int c = getTotalNumInputChannels(); c < getTotalNumOutputChannels(); ++c)
        buffer.clear (c, 0, buffer.getNumSamples());
}

juce::String ChannelProcessor::rackParamFor (int control) const
{
    if (boundId.isEmpty())
        return {};
    static const char* const mic[numControls] = { "gain", "phantom", "hpf", "insert", "softclip" };
    static const char* const dl5[numControls] = { "gain", "phantom", "hpf", "dcs", "cms" };
    if (boundKind == "dl5")
        return boundId + "." + dl5[control];
    return boundId + "." + mic[control];
}

juce::String ChannelProcessor::controlName (int control) const
{
    static const char* const mic[numControls] = { "Gain", "48 V", "HPF", "Insert", "Soft clip" };
    static const char* const dl5[numControls] = { "Gain", "48 V", "HPF", "DC servo", "CM servo" };
    return boundKind == "dl5" ? dl5[control] : mic[control];
}

int ChannelProcessor::controlIndex (const juce::String& paramId) const
{
    for (int i = 0; i < numControls; ++i)
        if (paramId == paramIds[i])
            return i;
    return -1;
}

// JUCE 7 has plain members here, JUCE 8 std::optional ones
static juce::String nameOf (const juce::String& s) { return s; }
static juce::String nameOf (const std::optional<juce::String>& s) { return s.value_or (juce::String()); }

void ChannelProcessor::updateTrackProperties (const TrackProperties& p)
{
    {
        const juce::ScopedLock sl (trackLock);
        trackName = nameOf (p.name);
    }
    rackClient->setTrack (boundId, getTrackName());
}

juce::String ChannelProcessor::getTrackName() const
{
    const juce::ScopedLock sl (trackLock);
    return trackName;
}

void ChannelProcessor::bindChannel (const juce::String& channelId)
{
    boundId = channelId;
    rackClient->setTrack (boundId, getTrackName());
    remote21::Channel ch;
    boundKind = rackClient->findChannel (channelId, ch) ? ch.kind : juce::String();
    for (auto& d : dirty)
        d = false;
    recallPending = false;
    pullFromRack (true);
    updateHostDisplay (ChangeDetails().withParameterInfoChanged (true));
}

// host/UI -> rack: listener may run on the audio thread, so only flag it
void ChannelProcessor::parameterChanged (const juce::String& id, float)
{
    if (applyingRemote.load())
        return;
    int i = controlIndex (id);
    if (i >= 0)
        dirty[i] = true;
}

void ChannelProcessor::timerCallback()
{
    if (boundId.isEmpty() || ! rackClient->isConnected())
        return;
    remote21::Channel ch;
    if (! rackClient->findChannel (boundId, ch))
        return;
    if (ch.kind != boundKind)
    {
        boundKind = ch.kind;
        updateHostDisplay (ChangeDetails().withParameterInfoChanged (true));
    }
    if (recallPending)
    {
        recallPending = false;
        for (int i = 0; i < numControls; ++i)
            if (i != 1) // never switch 48 V on its own when a project opens
                dirty[i] = true;
    }
    if (ch.linked)
        return; // the slave of a stereo link follows its master
    for (int i = 0; i < numControls; ++i)
    {
        if (! dirty[i].exchange (false))
            continue;
        auto* p = apvts.getParameter (paramIds[i]);
        float v = p->convertFrom0to1 (p->getValue());
        rackClient->set (rackParamFor (i), i == 0 ? juce::roundToInt (v) : (v >= 0.5f ? 1 : 0));
    }
}

// rack -> host
void ChannelProcessor::changeListenerCallback (juce::ChangeBroadcaster*)
{
    if (boundKind.isEmpty() && boundId.isNotEmpty())
    {
        remote21::Channel ch;
        if (rackClient->findChannel (boundId, ch))
            boundKind = ch.kind;
    }
    if (! recallPending)
        pullFromRack (false);
}

void ChannelProcessor::pullFromRack (bool force)
{
    if (boundId.isEmpty())
        return;
    for (int i = 0; i < numControls; ++i)
    {
        auto rid = rackParamFor (i);
        remote21::Param rp;
        if (! rackClient->getParam (rid, rp))
            continue;
        if (! force && (dirty[i].load() || rackClient->secondsSinceLocalChange (rid) < 0.35))
            continue; // our own change is still on its way
        auto* p = apvts.getParameter (paramIds[i]);
        float norm = p->convertTo0to1 ((float) rp.value);
        if (std::abs (norm - p->getValue()) < 1.0e-4f)
            continue;
        applyingRemote = true;
        p->beginChangeGesture();
        p->setValueNotifyingHost (norm);
        p->endChangeGesture();
        applyingRemote = false;
    }
}

void ChannelProcessor::getStateInformation (juce::MemoryBlock& dest)
{
    auto state = apvts.copyState();
    state.setProperty ("channel", boundId, nullptr);
    state.setProperty ("kind", boundKind, nullptr);
    state.setProperty ("recall", recallOnLoad, nullptr);
    if (auto xml = state.createXml())
        copyXmlToBinary (*xml, dest);
}

void ChannelProcessor::setStateInformation (const void* data, int size)
{
    auto xml = getXmlFromBinary (data, size);
    if (xml == nullptr)
        return;
    auto state = juce::ValueTree::fromXml (*xml);
    if (! state.isValid())
        return;
    applyingRemote = true;
    apvts.replaceState (state);
    applyingRemote = false;
    boundId = state.getProperty ("channel", "").toString();
    boundKind = state.getProperty ("kind", "").toString();
    recallOnLoad = (bool) state.getProperty ("recall", false);
    rackClient->setTrack (boundId, getTrackName());
    // with "recall" on, the project's settings are sent to the rack once the
    // channel is available; otherwise the plug-in shows what the rack has
    recallPending = recallOnLoad && boundId.isNotEmpty();
    if (! recallPending)
        pullFromRack (true);
}

juce::AudioProcessorEditor* ChannelProcessor::createEditor() { return new ChannelEditor (*this); }

juce::AudioProcessor* JUCE_CALLTYPE createPluginFilter() { return new ChannelProcessor(); }
