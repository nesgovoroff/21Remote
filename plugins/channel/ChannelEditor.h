// SPDX-License-Identifier: AGPL-3.0-or-later
#pragma once

#include <juce_audio_processors/juce_audio_processors.h>
#include "../common/Style.h"
#include "ChannelProcessor.h"

class ChannelEditor : public juce::AudioProcessorEditor, private juce::ChangeListener, private juce::Timer
{
public:
    explicit ChannelEditor (ChannelProcessor&);
    ~ChannelEditor() override;

    void paint (juce::Graphics&) override;
    void resized() override;

private:
    void changeListenerCallback (juce::ChangeBroadcaster*) override;
    void timerCallback() override { refresh(); }
    void refresh();
    void rebuildChannelList();

    struct FaderLook : public remote21::LookAndFeel
    {
        int minDb = -11, maxDb = 75, rangeLimit = 21;
        int getSliderThumbRadius (juce::Slider&) override { return 23; }
        void drawLinearSlider (juce::Graphics&, int x, int y, int w, int h, float pos, float, float,
                               juce::Slider::SliderStyle, juce::Slider&) override;
    };

    ChannelProcessor& proc;
    remote21::LookAndFeel look;
    FaderLook faderLook;

    juce::ComboBox channelBox;
    juce::Label status, gainReadout, commentTitle;
    juce::TextEditor comment;
    void commitComment();
    juce::Slider gain { juce::Slider::LinearVertical, juce::Slider::NoTextBox };
    juce::TextButton buttons[4];
    juce::ToggleButton recall { "Send these settings to the rack when the project opens" };
    std::unique_ptr<juce::AudioProcessorValueTreeState::SliderAttachment> gainAttach;
    std::unique_ptr<juce::AudioProcessorValueTreeState::ButtonAttachment> buttonAttach[4];

    juce::StringArray channelIds;
    juce::String lastListSignature;
    remote21::Channel current;
    bool present = false;

    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR (ChannelEditor)
};
