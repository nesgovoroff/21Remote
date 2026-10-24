// SPDX-License-Identifier: AGPL-3.0-or-later
#include "OverviewEditor.h"

using namespace remote21;

OverviewEditor::OverviewEditor (OverviewProcessor& p) : juce::AudioProcessorEditor (p), proc (p)
{
    setLookAndFeel (&look);

    slotLabel.setFont (condensed (15.0f));
    slotLabel.setColour (juce::Label::textColourId, colours::alu);
    addAndMakeVisible (slotLabel);

    slotBox.onChange = [this] { rebuildParamBox(); };
    addAndMakeVisible (slotBox);

    paramBox.setTextWhenNothingSelected ("Assign a rack control to this slot");
    paramBox.onChange = [this] {
        int slot = slotBox.getSelectedItemIndex();
        int idx = paramBox.getSelectedItemIndex();
        if (slot >= 0 && idx >= 0 && idx < paramIds.size() && paramIds[idx] != proc.getAssignment (slot))
            proc.assign (slot, paramIds[idx]);
        refresh();
    };
    addAndMakeVisible (paramBox);

    clearButton.onClick = [this] {
        int slot = slotBox.getSelectedItemIndex();
        if (slot >= 0)
            proc.assign (slot, {});
        paramBox.setSelectedId (0, juce::dontSendNotification);
        refresh();
    };
    addAndMakeVisible (clearButton);

    status.setFont (juce::FontOptions (12.0f));
    status.setColour (juce::Label::textColourId, colours::dim);
    status.setJustificationType (juce::Justification::centredRight);
    addAndMakeVisible (status);

    web = std::make_unique<juce::WebBrowserComponent>();
    addChildComponent (*web);

    proc.client().addChangeListener (this);
    setResizable (true, true);
    setResizeLimits (720, 480, 3000, 2000);
    setSize (1280, 800);
    rebuildSlotBox();
    slotBox.setSelectedItemIndex (0, juce::dontSendNotification);
    rebuildParamBox();
    refresh();
    startTimerHz (2);
}

OverviewEditor::~OverviewEditor()
{
    proc.client().removeChangeListener (this);
    setLookAndFeel (nullptr);
}

void OverviewEditor::rebuildSlotBox()
{
    juce::StringArray names;
    for (int i = 0; i < OverviewProcessor::numSlots; ++i)
    {
        auto t = proc.slotTitle (i);
        names.add (juce::String (i + 1) + (t.isEmpty() ? "  (free)" : "  " + t));
    }
    auto sig = names.joinIntoString ("|");
    if (sig == slotSignature)
        return;
    slotSignature = sig;
    int sel = slotBox.getSelectedItemIndex();
    slotBox.clear (juce::dontSendNotification);
    for (int i = 0; i < names.size(); ++i)
        slotBox.addItem (names[i], i + 1);
    slotBox.setSelectedItemIndex (juce::jmax (0, sel), juce::dontSendNotification);
}

void OverviewEditor::rebuildParamBox()
{
    juce::StringArray ids, names;
    for (auto& p : proc.client().getParams())
        if (! p.readOnly)
        {
            ids.add (p.id);
            names.add (p.label);
        }
    auto sig = ids.joinIntoString ("|") + names.joinIntoString ("|");
    if (sig != paramSignature)
    {
        paramSignature = sig;
        paramIds = ids;
        paramBox.clear (juce::dontSendNotification);
        for (int i = 0; i < names.size(); ++i)
            paramBox.addItem (names[i], i + 1);
    }
    int slot = slotBox.getSelectedItemIndex();
    int idx = slot >= 0 ? paramIds.indexOf (proc.getAssignment (slot)) : -1;
    if (idx >= 0)
        paramBox.setSelectedItemIndex (idx, juce::dontSendNotification);
    else
        paramBox.setSelectedId (0, juce::dontSendNotification);
}

void OverviewEditor::refresh()
{
    auto& c = proc.client();
    rebuildSlotBox();
    if (! paramBox.isPopupActive())
        rebuildParamBox();

    juce::String st;
    if (! c.isConnected())
        st = c.getStatus();
    else if (! c.isRackOnline())
        st = "Rack not connected. Check the connection in Settings.";
    else
        st = c.getRackName() + " connected. Slots appear in the DAW as automatable parameters.";
    status.setText (st, juce::dontSendNotification);

    auto url = c.isConnected() ? c.getUiUrl() : juce::String();
    if (url.isNotEmpty() && url != loadedUrl)
    {
        loadedUrl = url;
        web->goToURL (url);
    }
    web->setVisible (url.isNotEmpty());
    repaint();
}

void OverviewEditor::paint (juce::Graphics& g)
{
    g.fillAll (colours::graphite);
    g.setColour (juce::Colour (0xff1f2225));
    g.fillRect (0, 44, getWidth(), 1);
    if (! web->isVisible())
    {
        g.setColour (colours::dim);
        g.setFont (juce::FontOptions (15.0f));
        g.drawFittedText ("Waiting for 21 Remote.\nThe plug-in starts the app by itself; this can take a few seconds.",
                          getLocalBounds().withTrimmedTop (45).reduced (40), juce::Justification::centred, 3);
    }
}

void OverviewEditor::resized()
{
    auto r = getLocalBounds();
    auto bar = r.removeFromTop (44).reduced (10, 8);
    slotLabel.setBounds (bar.removeFromLeft (110));
    slotBox.setBounds (bar.removeFromLeft (260));
    bar.removeFromLeft (8);
    paramBox.setBounds (bar.removeFromLeft (300));
    bar.removeFromLeft (6);
    clearButton.setBounds (bar.removeFromLeft (64));
    bar.removeFromLeft (10);
    status.setBounds (bar);
    r.removeFromTop (1);
    web->setBounds (r);
}
