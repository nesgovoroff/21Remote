cmake_minimum_required (VERSION 3.22)

set (CMAKE_OSX_DEPLOYMENT_TARGET "11.0" CACHE STRING "")
set (CMAKE_OSX_ARCHITECTURES "arm64;x86_64" CACHE STRING "")

file (STRINGS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION" R21_VERSION LIMIT_COUNT 1)
project (RemotePlugins VERSION ${R21_VERSION} LANGUAGES C CXX)
set (CMAKE_CXX_STANDARD 17)
set (CMAKE_CXX_STANDARD_REQUIRED ON)

include (FetchContent)
FetchContent_Declare (juce
    GIT_REPOSITORY https://github.com/juce-framework/JUCE.git
    GIT_TAG 8.0.4
    GIT_SHALLOW ON)
FetchContent_MakeAvailable (juce)

set (COMMON_SOURCES common/RemoteClient.cpp)

function (remote21_plugin target product code bundle)
    juce_add_plugin (${target}
        PRODUCT_NAME "${product}"
        COMPANY_NAME "21 Remote"
        BUNDLE_ID "${bundle}"
        PLUGIN_MANUFACTURER_CODE Rm21
        PLUGIN_CODE ${code}
        FORMATS AU VST3
        IS_SYNTH FALSE
        NEEDS_MIDI_INPUT FALSE
        NEEDS_MIDI_OUTPUT FALSE
        IS_MIDI_EFFECT FALSE
        EDITOR_WANTS_KEYBOARD_FOCUS TRUE
        COPY_PLUGIN_AFTER_BUILD FALSE
        AU_MAIN_TYPE kAudioUnitType_Effect
        VST3_CATEGORIES Fx Tools
        HARDENED_RUNTIME_ENABLED FALSE)
    target_compile_definitions (${target} PUBLIC
        JUCE_WEB_BROWSER=1
        JUCE_USE_CURL=0
        JUCE_VST3_CAN_REPLACE_VST2=0
        JUCE_DISPLAY_SPLASH_SCREEN=0)
    target_link_libraries (${target} PRIVATE
        juce::juce_audio_utils
        juce::juce_gui_extra
        PUBLIC
        juce::juce_recommended_config_flags
        juce::juce_recommended_lto_flags
        juce::juce_recommended_warning_flags)
endfunction ()

remote21_plugin (RemoteChannel "21 Channel" R21c local.remote21.channel)
target_sources (RemoteChannel PRIVATE channel/ChannelProcessor.cpp channel/ChannelEditor.cpp ${COMMON_SOURCES})

remote21_plugin (RemoteOverview "21 Overview" R21o local.remote21.overview)
target_sources (RemoteOverview PRIVATE overview/OverviewProcessor.cpp overview/OverviewEditor.cpp ${COMMON_SOURCES})
