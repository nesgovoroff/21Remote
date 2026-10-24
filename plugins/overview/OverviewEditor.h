// SPDX-License-Identifier: AGPL-3.0-or-later
#pragma once

#include <juce_audio_processors/juce_audio_processors.h>
#include <juce_gui_extra/juce_gui_extra.h>
#include "../common/Style.h"
#include "OverviewProcessor.h"

class OverviewEditor : public juce::AudioProcessorEditor, private juce::ChangeListener, private juce::Timer
{
public:
    explicit OverviewEditor (OverviewProcessor&);
    ~OverviewEditor() override;

    void paint (juce::Graphics&) override;
    void resized() override;

private:
    void changeListenerCallback (juce::ChangeBroadcaster*) override { refresh(); }
    void timerCallback() override { refresh(); }
    void refresh();
    void rebuildSlotBox();
    void rebuildParamBox();

    OverviewProcessor& proc;
    remote21::LookAndFeel look;
    juce::Label slotLabel { {}, "Automation slot" }, status;
    juce::ComboBox slotBox, paramBox;
    juce::TextButton clearButton { "Clear" };
    std::unique_ptr<juce::WebBrowserComponent> web;
    juce::String loadedUrl;
    juce::StringArray paramIds;
    juce::String paramSignature, slotSignature;

    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR (OverviewEditor)
};
