// SPDX-License-Identifier: AGPL-3.0-or-later
// Shared look of the 21 Remote plug-ins: graphite panels, aluminium text, LED colours,
// matching the 21 Remote app.
#pragma once

#include <juce_gui_basics/juce_gui_basics.h>

namespace remote21
{
namespace colours
{
    const juce::Colour graphite { 0xff2a2d31 }, panel { 0xff33373c }, raised { 0xff464b51 }, line { 0xff50565d },
        alu { 0xffc9cdd1 }, ink { 0xffe8eaec }, dim { 0xff9aa1a8 }, led { 0xff57d36b }, amber { 0xfff2a33a },
        red { 0xffe5484d }, slit { 0xff111315 }, micZone { 0xff5a5e63 }, lineZone { 0xff8f9499 };
}

class LookAndFeel : public juce::LookAndFeel_V4
{
public:
    LookAndFeel()
    {
        using namespace colours;
        setColour (juce::ResizableWindow::backgroundColourId, graphite);
        setColour (juce::ComboBox::backgroundColourId, panel);
        setColour (juce::ComboBox::outlineColourId, line);
        setColour (juce::ComboBox::textColourId, ink);
        setColour (juce::ComboBox::arrowColourId, dim);
        setColour (juce::PopupMenu::backgroundColourId, juce::Colour (0xff3b4046));
        setColour (juce::PopupMenu::textColourId, ink);
        setColour (juce::PopupMenu::highlightedBackgroundColourId, juce::Colour (0xff3d6fb6));
        setColour (juce::Label::textColourId, ink);
        setColour (juce::TextButton::buttonColourId, panel);
        setColour (juce::TextButton::textColourOffId, dim);
        setColour (juce::TextButton::textColourOnId, juce::Colour (0xff141516));
        setColour (juce::ToggleButton::textColourId, ink);
        setColour (juce::ToggleButton::tickColourId, alu);
        setColour (juce::ToggleButton::tickDisabledColourId, dim);
    }

    juce::Font getTextButtonFont (juce::TextButton&, int h) override
    {
        return juce::Font (juce::FontOptions ("Avenir Next Condensed", (float) juce::jmin (15, h - 6), juce::Font::bold));
    }

    void drawButtonBackground (juce::Graphics& g, juce::Button& b, const juce::Colour&, bool over, bool down) override
    {
        auto r = b.getLocalBounds().toFloat().reduced (0.5f);
        auto on = b.getToggleState();
        auto c = on ? b.findColour (juce::TextButton::buttonOnColourId) : b.findColour (juce::TextButton::buttonColourId);
        if (! b.isEnabled())
            c = c.withMultipliedAlpha (0.5f);
        else if (down || over)
            c = c.brighter (0.08f);
        g.setColour (c);
        g.fillRoundedRectangle (r, 4.0f);
        g.setColour (on ? c : colours::line);
        g.drawRoundedRectangle (r, 4.0f, 1.0f);
    }
};

inline juce::Font condensed (float size, bool bold = true)
{
    return juce::Font (juce::FontOptions ("Avenir Next Condensed", size, bold ? juce::Font::bold : juce::Font::plain));
}

// text colour readable on a strip colour
inline juce::Colour textOn (juce::Colour c)
{
    return c.getRed() + c.getGreen() + c.getBlue() < 0x180 ? juce::Colours::white : juce::Colour (0xff111111);
}

} // namespace remote21
