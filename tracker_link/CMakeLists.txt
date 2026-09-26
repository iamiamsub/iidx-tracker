cmake_minimum_required(VERSION 3.31)

# tracker_link.dll: a hook DLL for the game that adds what the tracker needs to the game's own
# requests (see src/tracker_link.cc). Built with MSVC; see build.ps1.
project(tracker_link VERSION 1.1.0 LANGUAGES CXX RC)

find_package(Zydis CONFIG REQUIRED)
find_package(safetyhook CONFIG REQUIRED)

configure_file(src/version.rc.in ${CMAKE_BINARY_DIR}/version.rc @ONLY)

add_library(tracker_link SHARED
    src/tracker_link.cc
    src/memory.cc
    src/modules.cc
    ${CMAKE_BINARY_DIR}/version.rc
)

target_compile_features(tracker_link PRIVATE cxx_std_23)

target_link_directories(tracker_link PRIVATE
    ${CMAKE_SOURCE_DIR}/lib
)

target_link_libraries(tracker_link PRIVATE
    avs2-core
    safetyhook::safetyhook
)

# Reproducible output: the same source always gives the same file (no timestamps).
if (MSVC)
    target_compile_options(tracker_link PRIVATE /Brepro)
    target_link_options(tracker_link PRIVATE /Brepro)
endif ()

target_compile_definitions(tracker_link PRIVATE
    "META_PROJECT_VERSION=\"${PROJECT_VERSION}\""
)
