// SPDX-License-Identifier: AGPL-3.0-or-later
// "21 Overview": the whole rack inside the DAW (the app's interface in a
// web view) plus 16 automation slots that can be assigned to any rack control.
#pragma once

#include <juce_audio_processors/juce_audio_processors.h>
#include "../common/RemoteClient.h"

class OverviewProcessor : public juce::AudioProcessor, private juce::ChangeListener, private juce::Timer
{
public:
    static constexpr int numSlots = 16;

    OverviewProcessor();
    ~OverviewProcessor() override;

    remote21::Client& client() { return *rackClient; }
    juce::String getAssignment (int slot) const;
    void assign (int slot, const juce::String& rackParamId); // "" clears
    juce::String slotTitle (int slot) const;                 // e.g. "MIC 1/1 (Kick) Gain"

    // AudioProcessor
    void prepareToPlay (double, int) override {}
    void releaseResources() override {}
    bool isBusesLayoutSupported (const BusesLayout&) const override;
    void processBlock (juce::AudioBuffer<float>&, juce::MidiBuffer&) override;
    juce::AudioProcessorEditor* createEditor() override;
    bool hasEditor() const override { return true; }
    const juce::String getName() const override { return "21 Overview"; }
    bool acceptsMidi() const override { return false; }
    bool producesMidi() const override { return false; }
    double getTailLengthSeconds() const override { return 0.0; }
    int getNumPrograms() override { return 1; }
    int getCurrentProgram() override { return 0; }
    void setCurrentProgram (int) override {}
    const juce::String getProgramName (int) override { return {}; }
    void changeProgramName (int, const juce::String&) override {}
    void getStateInformation (juce::MemoryBlock&) override;
    void setStateInformation (const void*, int) override;

    // value formatting for the host (called from SlotParameter)
    juce::String formatSlot (int slot, float normalised) const;

private:
    class SlotParameter;
    void changeListenerCallback (juce::ChangeBroadcaster*) override;
    void timerCallback() override;
    void pullFromRack();
    void slotChanged (int slot);
    static int toRack (const remote21::Param&, float norm);
    static float fromRack (const remote21::Param&);

    std::unique_ptr<remote21::Client> rackClient;
    juce::Array<SlotParameter*> slots;
    mutable juce::CriticalSection assignLock;
    juce::String assignment[numSlots];
    std::atomic<bool> dirty[numSlots] {};
    std::atomic<bool> applyingRemote { false };

    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR (OverviewProcessor)
};
