// SPDX-License-Identifier: AGPL-3.0-or-later
#include "OverviewProcessor.h"
#include "OverviewEditor.h"

// A 0..1 host parameter whose name and value text follow its assignment.
class OverviewProcessor::SlotParameter : public juce::AudioParameterFloat
{
public:
    SlotParameter (OverviewProcessor& o, int index)
        : juce::AudioParameterFloat (juce::ParameterID { "slot" + juce::String (index + 1), 1 },
                                     "Slot " + juce::String (index + 1), 0.0f, 1.0f, 0.0f),
          owner (o), slot (index)
    {
    }

    juce::String getName (int maxLen) const override
    {
        auto t = owner.slotTitle (slot);
        auto n = juce::String (slot + 1) + (t.isEmpty() ? juce::String (" (unassigned)") : ": " + t);
        return n.substring (0, maxLen);
    }

    juce::String getText (float v, int maxLen) const override { return owner.formatSlot (slot, v).substring (0, maxLen); }

    void valueChanged (float) override { owner.slotChanged (slot); }

private:
    OverviewProcessor& owner;
    int slot;
};

OverviewProcessor::OverviewProcessor()
    : juce::AudioProcessor (BusesProperties()
                                .withInput ("Input", juce::AudioChannelSet::stereo(), true)
                                .withOutput ("Output", juce::AudioChannelSet::stereo(), true))
{
    for (int i = 0; i < numSlots; ++i)
    {
        auto* p = new SlotParameter (*this, i);
        slots.add (p);
        addParameter (p);
    }
    rackClient = std::make_unique<remote21::Client> ("21 Overview");
    rackClient->addChangeListener (this);
    startTimerHz (30);
}

OverviewProcessor::~OverviewProcessor()
{
    stopTimer();
    rackClient->removeChangeListener (this);
    rackClient.reset();
}

bool OverviewProcessor::isBusesLayoutSupported (const BusesLayout& layouts) const
{
    auto in = layouts.getMainInputChannelSet(), out = layouts.getMainOutputChannelSet();
    return in == out && (out == juce::AudioChannelSet::mono() || out == juce::AudioChannelSet::stereo());
}

void OverviewProcessor::processBlock (juce::AudioBuffer<float>& buffer, juce::MidiBuffer&)
{
    for (int c = getTotalNumInputChannels(); c < getTotalNumOutputChannels(); ++c)
        buffer.clear (c, 0, buffer.getNumSamples());
}

juce::String OverviewProcessor::getAssignment (int slot) const
{
    const juce::ScopedLock sl (assignLock);
    return juce::isPositiveAndBelow (slot, numSlots) ? assignment[slot] : juce::String();
}

juce::String OverviewProcessor::slotTitle (int slot) const
{
    auto id = getAssignment (slot);
    if (id.isEmpty())
        return {};
    remote21::Param p;
    if (rackClient != nullptr && rackClient->getParam (id, p))
        return p.label;
    return id + " (not in the rack)";
}

int OverviewProcessor::toRack (const remote21::Param& p, float norm)
{
    if (p.kind == "bool")
        return norm >= 0.5f ? 1 : 0;
    return p.min + juce::roundToInt (norm * (float) (p.max - p.min));
}

float OverviewProcessor::fromRack (const remote21::Param& p)
{
    if (p.max <= p.min)
        return 0.0f;
    return (float) (p.value - p.min) / (float) (p.max - p.min);
}

juce::String OverviewProcessor::formatSlot (int slot, float norm) const
{
    auto id = getAssignment (slot);
    remote21::Param p;
    if (id.isEmpty() || rackClient == nullptr || ! rackClient->getParam (id, p))
        return juce::String (norm, 2);
    int v = toRack (p, norm);
    if (p.kind == "bool")
        return v ? "On" : "Off";
    if (p.kind == "range")
        return (v > 0 ? "+" : "") + juce::String (v) + " dB";
    if (id.contains (".fs"))
        return juce::StringArray { "44.1 kHz", "48 kHz", "96 kHz", "ext" }[juce::jlimit (0, 3, v)];
    if (id.contains (".wl"))
        return juce::StringArray { "16 bit", "18 bit", "20 bit", "24 bit" }[juce::jlimit (0, 3, v)];
    return juce::String (v);
}

void OverviewProcessor::assign (int slot, const juce::String& rackParamId)
{
    if (! juce::isPositiveAndBelow (slot, numSlots))
        return;
    {
        const juce::ScopedLock sl (assignLock);
        assignment[slot] = rackParamId;
    }
    dirty[slot] = false;
    // show the rack's current value; assigning never changes the rack
    remote21::Param p;
    if (rackParamId.isNotEmpty() && rackClient->getParam (rackParamId, p))
    {
        applyingRemote = true;
        slots[slot]->setValueNotifyingHost (fromRack (p));
        applyingRemote = false;
    }
    updateHostDisplay (ChangeDetails().withParameterInfoChanged (true));
}

// host automation may call this on the audio thread: only flag it
void OverviewProcessor::slotChanged (int slot)
{
    if (! applyingRemote.load())
        dirty[slot] = true;
}

void OverviewProcessor::timerCallback()
{
    if (! rackClient->isConnected())
        return;
    for (int i = 0; i < numSlots; ++i)
    {
        if (! dirty[i].exchange (false))
            continue;
        auto id = getAssignment (i);
        remote21::Param p;
        if (id.isEmpty() || ! rackClient->getParam (id, p) || p.readOnly)
            continue;
        rackClient->set (id, toRack (p, slots[i]->get()));
    }
}

void OverviewProcessor::changeListenerCallback (juce::ChangeBroadcaster*)
{
    pullFromRack();
    updateHostDisplay (ChangeDetails().withParameterInfoChanged (true));
}

void OverviewProcessor::pullFromRack()
{
    for (int i = 0; i < numSlots; ++i)
    {
        auto id = getAssignment (i);
        remote21::Param p;
        if (id.isEmpty() || dirty[i].load() || ! rackClient->getParam (id, p) || rackClient->secondsSinceLocalChange (id) < 0.35)
            continue;
        // compare in rack units so scaling rounding does not cause churn
        if (toRack (p, slots[i]->get()) == p.value)
            continue;
        applyingRemote = true;
        slots[i]->beginChangeGesture();
        slots[i]->setValueNotifyingHost (fromRack (p));
        slots[i]->endChangeGesture();
        applyingRemote = false;
    }
}

void OverviewProcessor::getStateInformation (juce::MemoryBlock& dest)
{
    juce::XmlElement xml ("RemoteOverview");
    for (int i = 0; i < numSlots; ++i)
    {
        auto* e = xml.createNewChildElement ("Slot");
        e->setAttribute ("index", i);
        e->setAttribute ("param", getAssignment (i));
        e->setAttribute ("value", (double) slots[i]->get());
    }
    copyXmlToBinary (xml, dest);
}

void OverviewProcessor::setStateInformation (const void* data, int size)
{
    auto xml = getXmlFromBinary (data, size);
    if (xml == nullptr || xml->getChildByName ("Slot") == nullptr)
        return;
    for (auto* e : xml->getChildWithTagNameIterator ("Slot"))
    {
        int i = e->getIntAttribute ("index", -1);
        if (! juce::isPositiveAndBelow (i, numSlots))
            continue;
        {
            const juce::ScopedLock sl (assignLock);
            assignment[i] = e->getStringAttribute ("param");
        }
        applyingRemote = true;
        slots[i]->setValueNotifyingHost ((float) e->getDoubleAttribute ("value", 0.0));
        applyingRemote = false;
    }
    pullFromRack(); // the rack is the reference; the project's automation drives it from here on
    updateHostDisplay (ChangeDetails().withParameterInfoChanged (true));
}

juce::AudioProcessorEditor* OverviewProcessor::createEditor() { return new OverviewEditor (*this); }

juce::AudioProcessor* JUCE_CALLTYPE createPluginFilter() { return new OverviewProcessor(); }
