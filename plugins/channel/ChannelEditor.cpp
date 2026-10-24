// SPDX-License-Identifier: AGPL-3.0-or-later
#include "ChannelEditor.h"

using namespace remote21;

ChannelEditor::ChannelEditor (ChannelProcessor& p) : juce::AudioProcessorEditor (p), proc (p)
{
    setLookAndFeel (&look);

    channelBox.setTextWhenNothingSelected ("Choose a channel of the rack");
    channelBox.setTextWhenNoChoicesAvailable ("No microphone channels in the rack");
    channelBox.onChange = [this] {
        int idx = channelBox.getSelectedItemIndex();
        if (idx >= 0 && idx < channelIds.size() && channelIds[idx] != proc.getBoundChannel())
            proc.bindChannel (channelIds[idx]);
        refresh();
    };
    addAndMakeVisible (channelBox);

    status.setFont (juce::FontOptions (12.0f));
    status.setColour (juce::Label::textColourId, colours::dim);
    status.setJustificationType (juce::Justification::centredLeft);
    addAndMakeVisible (status);

    gain.setLookAndFeel (&faderLook);
    gain.setMouseDragSensitivity (400);
    gain.setVelocityBasedMode (false);
    gain.setDoubleClickReturnValue (true, 0.0);
    addAndMakeVisible (gain);
    gainAttach = std::make_unique<juce::AudioProcessorValueTreeState::SliderAttachment> (proc.apvts, "gain", gain);
    gain.onValueChange = [this] { refresh(); };

    gainReadout.setFont (condensed (24.0f));
    gainReadout.setJustificationType (juce::Justification::centred);
    addAndMakeVisible (gainReadout);

    for (int i = 0; i < 4; ++i)
    {
        auto& b = buttons[i];
        b.setClickingTogglesState (true);
        b.setColour (juce::TextButton::buttonOnColourId, i == 0 ? colours::amber : colours::alu);
        addAndMakeVisible (b);
        buttonAttach[i] = std::make_unique<juce::AudioProcessorValueTreeState::ButtonAttachment> (
            proc.apvts, ChannelProcessor::paramIds[i + 1], b);
    }
    buttons[0].setTooltip ("Phantom power. Not automatable, so automation can never switch it.");

    recall.setToggleState (proc.getRecallOnLoad(), juce::dontSendNotification);
    recall.setTooltip ("Off: the plug-in shows what the rack is set to. On: when the project opens, gain, HPF and the "
                       "other switches are sent to the rack (48 V is never switched automatically).");
    recall.onClick = [this] { proc.setRecallOnLoad (recall.getToggleState()); };
    addAndMakeVisible (recall);

    commentTitle.setText ("Comment", juce::dontSendNotification);
    commentTitle.setFont (condensed (13.0f));
    commentTitle.setColour (juce::Label::textColourId, colours::dim);
    addAndMakeVisible (commentTitle);
    comment.setMultiLine (true, true);
    comment.setReturnKeyStartsNewLine (false);
    comment.setTextToShowWhenEmpty ("Notes for this channel (kept in 21 Remote)", colours::dim.withAlpha (0.7f));
    comment.setFont (juce::FontOptions (13.0f));
    comment.setColour (juce::TextEditor::backgroundColourId, juce::Colour (0xff26292c));
    comment.setColour (juce::TextEditor::outlineColourId, colours::line);
    comment.setColour (juce::TextEditor::focusedOutlineColourId, juce::Colour (0xff7fb4ff));
    comment.setColour (juce::TextEditor::textColourId, colours::ink);
    comment.onReturnKey = [this] { commitComment(); unfocusAllComponents(); };
    comment.onEscapeKey = [this] { comment.setText (current.comment, false); unfocusAllComponents(); };
    comment.onFocusLost = [this] { commitComment(); };
    addAndMakeVisible (comment);

    proc.client().addChangeListener (this);
    setSize (300, 640);
    setResizable (true, true);
    setResizeLimits (260, 500, 600, 1200);
    rebuildChannelList();
    refresh();
    startTimerHz (4);
}

ChannelEditor::~ChannelEditor()
{
    proc.client().removeChangeListener (this);
    gain.setLookAndFeel (nullptr);
    setLookAndFeel (nullptr);
}

void ChannelEditor::changeListenerCallback (juce::ChangeBroadcaster*)
{
    rebuildChannelList();
    refresh();
}

void ChannelEditor::rebuildChannelList()
{
    juce::StringArray ids, names;
    for (auto& c : proc.client().getChannels())
        if (c.kind == "mic" || c.kind == "dl5")
        {
            ids.add (c.id);
            names.add (c.displayName() + (c.linked ? "  (stereo-linked)" : ""));
        }
    auto sig = ids.joinIntoString ("|") + names.joinIntoString ("|");
    if (sig != lastListSignature)
    {
        lastListSignature = sig;
        channelIds = ids;
        channelBox.clear (juce::dontSendNotification);
        for (int i = 0; i < names.size(); ++i)
            channelBox.addItem (names[i], i + 1);
    }
    int idx = channelIds.indexOf (proc.getBoundChannel());
    if (idx >= 0)
        channelBox.setSelectedItemIndex (idx, juce::dontSendNotification);
    else if (proc.getBoundChannel().isNotEmpty())
        channelBox.setText (proc.getBoundChannel() + " (not in the rack)", juce::dontSendNotification);
}

void ChannelEditor::commitComment()
{
    if (present && comment.getText() != current.comment)
        proc.client().setComment (current.id, comment.getText());
}

void ChannelEditor::refresh()
{
    auto& c = proc.client();
    present = proc.getBoundChannel().isNotEmpty() && c.findChannel (proc.getBoundChannel(), current);

    juce::String st;
    if (! c.isConnected())
        st = c.getStatus();
    else if (! c.isRackOnline())
        st = "21 Remote is running, but the rack is not connected.";
    else if (proc.getBoundChannel().isEmpty())
        st = "Pick the channel this track records from.";
    else if (! present)
        st = "This channel is not in the rack right now.";
    else if (current.linked)
        st = "Stereo-linked: follows the channel on its left.";
    else
        st = c.getRackName() + "   channel " + current.ids;
    if (auto tn = proc.getTrackName(); tn.isNotEmpty() && present)
        st << "\nTrack: " << tn;
    status.setText (st, juce::dontSendNotification);

    comment.setEnabled (present && c.isConnected());
    if (! comment.hasKeyboardFocus (true) && comment.getText() != (present ? current.comment : juce::String()))
        comment.setText (present ? current.comment : juce::String(), false);

    bool live = present && c.isConnected() && ! current.linked;
    gain.setEnabled (live);
    for (int i = 0; i < 4; ++i)
    {
        buttons[i].setButtonText (proc.controlName (i + 1));
        buttons[i].setEnabled (live);
    }

    bool dl5 = present && current.kind == "dl5";
    faderLook.minDb = dl5 ? -13 : -11;
    faderLook.maxDb = dl5 ? 62 : 75;
    faderLook.rangeLimit = dl5 ? 17 : 21;
    int g = juce::roundToInt (gain.getValue());
    g = juce::jlimit (faderLook.minDb, faderLook.maxDb, g);
    gainReadout.setText ((g > 0 ? "+" : "") + juce::String (g) + " dB", juce::dontSendNotification);
    gainReadout.setColour (juce::Label::textColourId, live ? colours::ink : colours::dim);
    repaint();
}

void ChannelEditor::paint (juce::Graphics& g)
{
    g.fillAll (colours::graphite);
    auto r = getLocalBounds();

    // header strip with the channel's fixed name, like the console view
    auto head = r.removeFromTop (8);
    juce::ignoreUnused (head);

    // scribble strip at the bottom: user label on the channel colour
    auto scribble = getLocalBounds().removeFromBottom (44);
    auto col = present ? current.colour : colours::raised;
    if (present && current.linked)
        col = col.darker (0.35f);
    g.setColour (col);
    g.fillRect (scribble);
    g.setColour (textOn (col));
    g.setFont (condensed (17.0f));
    juce::String text = present ? (current.label.isNotEmpty() ? current.label : current.name) : juce::String ("21 Channel");
    g.drawFittedText (text, scribble.reduced (8, 2), juce::Justification::centred, 2);

    // fixed label above the fader
    if (present)
    {
        g.setColour (colours::alu);
        g.setFont (condensed (15.0f));
        g.drawText (current.name, gainReadout.getBounds().translated (0, -24), juce::Justification::centred);
    }
}

void ChannelEditor::resized()
{
    auto r = getLocalBounds().reduced (12, 10);
    channelBox.setBounds (r.removeFromTop (28));
    r.removeFromTop (4);
    status.setBounds (r.removeFromTop (32));
    r.removeFromBottom (44 - 10); // scribble strip
    r.removeFromBottom (6);
    comment.setBounds (r.removeFromBottom (48));
    commentTitle.setBounds (r.removeFromBottom (20));
    recall.setBounds (r.removeFromBottom (36));
    r.removeFromTop (26);
    gainReadout.setBounds (r.removeFromTop (30));

    auto buttonsCol = r.removeFromRight (juce::jmax (96, r.getWidth() / 3));
    buttonsCol.removeFromTop (6);
    for (auto& b : buttons)
    {
        b.setBounds (buttonsCol.removeFromTop (34).reduced (0, 3));
        buttonsCol.removeFromTop (4);
    }
    gain.setBounds (r.reduced (10, 6));
}

// vertical fader with the LINE / MIC bands and dB scale of the app
void ChannelEditor::FaderLook::drawLinearSlider (juce::Graphics& g, int x, int y, int w, int h, float pos, float, float,
                                                 juce::Slider::SliderStyle, juce::Slider& s)
{
    const float capH = 46.0f, half = capH * 0.5f;
    // same mapping the slider uses for the mouse, so the cap is where you grab it
    auto yFor = [&] (double v) { return s.getPositionOfValue (v); };
    juce::ignoreUnused (pos, y, h);
    float cx = (float) x + (float) w * 0.5f;

    // zones: LINE below rangeLimit dB, MIC above (only within this card's range)
    float bandX = (float) x + 4.0f;
    g.setColour (colours::micZone);
    g.fillRoundedRectangle (bandX, yFor (maxDb), 14.0f, yFor (rangeLimit) - yFor (maxDb), 2.0f);
    g.setColour (colours::lineZone);
    g.fillRoundedRectangle (bandX, yFor (rangeLimit), 14.0f, yFor (minDb) - yFor (rangeLimit), 2.0f);
    g.setFont (condensed (10.0f));
    g.setColour (colours::graphite);
    g.drawText ("MIC", juce::Rectangle<float> (bandX, yFor (maxDb) + 4, 14.0f, 14.0f), juce::Justification::centred);
    g.drawText ("LINE", juce::Rectangle<float> (bandX - 3, yFor (minDb) - 18, 20.0f, 14.0f), juce::Justification::centred);

    // slit
    g.setColour (colours::slit);
    g.fillRoundedRectangle (cx - 2.0f, yFor (maxDb), 4.0f, yFor (minDb) - yFor (maxDb), 2.0f);

    // scale
    g.setFont (condensed (11.0f, false));
    int step = juce::jmax (1, (maxDb - minDb) / 10);
    for (int v = minDb;; v += step)
    {
        int vv = juce::jmin (v, maxDb);
        float yy = yFor (vv);
        g.setColour (juce::Colour (0xff6b7178));
        g.drawHorizontalLine ((int) yy, cx + 16.0f, cx + 22.0f);
        g.setColour (colours::dim);
        g.drawText ((vv > 0 ? "+" : "") + juce::String (vv), juce::Rectangle<float> (cx + 24.0f, yy - 7.0f, 34.0f, 14.0f),
                    juce::Justification::centredLeft);
        if (vv >= maxDb)
            break;
    }

    // cap
    double v = juce::jlimit ((double) minDb, (double) maxDb, s.getValue());
    float cy = yFor (v);
    juce::Rectangle<float> cap (cx - 16.0f, cy - half, 32.0f, capH);
    g.setColour (juce::Colours::black.withAlpha (0.45f));
    g.fillRoundedRectangle (cap.translated (0, 3), 5.0f);
    juce::ColourGradient grad (juce::Colour (0xff6a7179), cap.getX(), cap.getY(), juce::Colour (0xff2f3337), cap.getX(), cap.getBottom(), false);
    g.setGradientFill (grad);
    g.fillRoundedRectangle (cap, 5.0f);
    g.setColour (s.isEnabled() ? colours::ink : colours::dim);
    g.fillRect (cap.getX() + 4.0f, cy - 1.0f, cap.getWidth() - 8.0f, 2.0f);
    g.setColour (juce::Colour (0xff1d1f22));
    g.drawRoundedRectangle (cap, 5.0f, 1.0f);
}
