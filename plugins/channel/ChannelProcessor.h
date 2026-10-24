// SPDX-License-Identifier: AGPL-3.0-or-later
// "21 Channel": put it on a track, pick a channel of the rack, control and
// automate its preamp (gain, 48 V, HPF, insert/DC servo, soft clip/CM servo).
// Audio passes through untouched.
#pragma once

#include <juce_audio_processors/juce_audio_processors.h>
#include "../common/RemoteClient.h"

class ChannelProcessor : public juce::AudioProcessor,
                         private juce::AudioProcessorValueTreeState::Listener,
                         private juce::ChangeListener,
                         private juce::Timer
{
public:
    ChannelProcessor();
    ~ChannelProcessor() override;

    // the five controls, in this order: gain, phantom, hpf, opt1, opt2
    static constexpr int numControls = 5;
    static const char* const paramIds[numControls];

    juce::AudioProcessorValueTreeState apvts;
    remote21::Client& client() { return *rackClient; }

    juce::String getBoundChannel() const { return boundId; }
    void bindChannel (const juce::String& channelId); // pulls the rack's values, does not change the rack
    bool getRecallOnLoad() const { return recallOnLoad; }
    void setRecallOnLoad (bool b) { recallOnLoad = b; }

    // rack parameter id for control i on the bound channel ("" if not applicable)
    juce::String rackParamFor (int control) const;
    juce::String controlName (int control) const;

    // AudioProcessor
    void prepareToPlay (double, int) override {}
    void releaseResources() override {}
    bool isBusesLayoutSupported (const BusesLayout&) const override;
    void processBlock (juce::AudioBuffer<float>&, juce::MidiBuffer&) override;
    juce::AudioProcessorEditor* createEditor() override;
    bool hasEditor() const override { return true; }
    const juce::String getName() const override { return "21 Channel"; }
    bool acceptsMidi() const override { return false; }
    bool producesMidi() const override { return false; }
    double getTailLengthSeconds() const override { return 0.0; }
    int getNumPrograms() override { return 1; }
    int getCurrentProgram() override { return 0; }
    void setCurrentProgram (int) override {}
    const juce::String getProgramName (int) override { return {}; }
    void changeProgramName (int, const juce::String&) override {}
    void updateTrackProperties (const TrackProperties&) override;
    juce::String getTrackName() const;
    void getStateInformation (juce::MemoryBlock&) override;
    void setStateInformation (const void*, int) override;

private:
    static juce::AudioProcessorValueTreeState::ParameterLayout createLayout();
    void parameterChanged (const juce::String& id, float newValue) override;
    void changeListenerCallback (juce::ChangeBroadcaster*) override;
    void timerCallback() override;
    void pullFromRack (bool force);
    int controlIndex (const juce::String& paramId) const;

    std::unique_ptr<remote21::Client> rackClient;
    juce::String boundId, boundKind;
    mutable juce::CriticalSection trackLock;
    juce::String trackName;
    bool recallOnLoad = false, recallPending = false;
    std::atomic<bool> applyingRemote { false };
    std::atomic<bool> dirty[numControls] {};

    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR (ChannelProcessor)
};
