// tracker_link.dll - gives an iidx-tracker in front of the server what the
// game's own traffic does not carry, by adding it to that traffic (the way
// upstream omnifix reports its hashes):
//
//   services.get  <tracker_link ver@ name@ size@ sha256@/>
//                 the music data file the game loads, so the tracker files the
//                 cabinet's plays under the right song list (a music ID can
//                 name different songs in different databases)
//   music.reg     <tracker_link ver@><judge/><lane/><measure/></tracker_link>
//                 the play's judgments by timing (FAST/SLOW) and by key, and its
//                 score rate per measure, which the game keeps to itself
//
// The tracker takes <tracker_link> out before passing a request on; a server
// reached directly ignores it, as it does upstream omnifix's element. There is
// no network code here - nothing but the game's own requests carries the data.
// Load it with "-k tracker_link.dll"; it works with or without altfix.
// Part of IIDX Tracker; the game internals it reads are described in the
// comments below (addresses are for bm2dx 2026081900).

#include <array>
#include <ctime>
#include <format>
#include <string>
#include <vector>
#include <ranges>
#include <cstdint>
#include <cstring>
#include <stdexcept>
#include <string_view>
#include <windows.h>
#include <safetyhook.hpp>

#include "avs2.h"
#include "hooks.h"
#include "memory.h"
#include "modules.h"
#include "sha256.h"
#include "exceptions.h"

using namespace omnifix;

namespace
{
    // The game's music data path. altfix rewrites the string in place for
    // omnimix, so it is read when services.get goes out, long after init.
    auto music_data_path = static_cast<const char*>(nullptr);

    // g_DeadState +0x94: int[2 lane sides][11] judgments by display code
    // (FAST POOR, FAST BAD, FAST GOOD, FAST GREAT, PGREAT, SLOW GREAT, SLOW GOOD,
    // SLOW BAD, SLOW POOR, 2 unused; analysis_0819 playanalyze4.md §2.2).
    auto judge_counts = static_cast<const std::uint8_t*>(nullptr);
    auto constexpr judge_counts_size = 2 * 11 * 4;

    // g_ScoreBlock +0x30: int[8 lanes][6 kinds] per lane side, the sides 0x2528
    // apart (keys 1-7 and the scratch; PG, GR, GD, BD, POOR, empty POOR).
    auto lane_counts = static_cast<const std::uint8_t*>(nullptr);
    auto constexpr lane_side_stride = 0x2528;
    auto constexpr lane_side_size = 8 * 6 * 4;

    // The result screen's score graph (StageResultDrawGraph::CopyGaugeHistory)
    // computes the score rate of each measure from the play's per-measure counts
    // after the result is saved; the counts are final as soon as the play ends,
    // so music.reg calls the same functions itself.
    using timeline_fn = const std::uint8_t* (*)();     // GetPtrTimeline; +0x64 = last measure
    using measure_fn = float (*)(int side, int measure); // CalcMeasureValue; -1 = no notes
    using level_fn = int (*)(int side, int measure);     // CalcLastMeasureDjLevel; 8 = no notes

    auto get_timeline = static_cast<timeline_fn>(nullptr);
    auto measure_value = static_cast<measure_fn>(nullptr);
    auto measure_level = static_cast<level_fn>(nullptr);

    auto services_get_hook = SafetyHookInline {};
    auto music_reg_hook = SafetyHookInline {};

    struct music_data_info
    {
        std::string name, size, sha256;
    };

    auto describe_music_data() -> music_data_info
    {
        if (music_data_path == nullptr)
            return {};

        auto const path = std::string_view { music_data_path };
        auto const data = avs2::file::read(path);

        if (data.empty())
            return {};

        return { std::string { path.substr(path.find_last_of('/') + 1) },
            std::to_string(data.size()), sha256_hex(data) };
    }

    /** The player side of a music.reg request (its pside@), or -1. */
    auto player_side(avs2::node_ptr node) -> int
    {
        char value[16] {};

        if (avs2::property_node_refer(nullptr, node, "pside@", avs2::node_type_attr, value, sizeof(value)) < 0)
            return -1;

        return value[0] == '0' || value[0] == '1' ? value[0] - '0' : -1;
    }

    /** The score rate (0..1, -1 = no notes) of every measure the side played, as the result graph shows it. */
    auto measure_rates(int side) -> std::vector<float>
    {
        auto const last = *reinterpret_cast<const std::int32_t*>(get_timeline() + 0x64);
        auto count = last + 1;

        // like CopyGaugeHistory: a closing measure without notes is left out
        if (count > 0 && measure_level(side, last) == 8)
            --count;

        auto rates = std::vector<float>(count < 0 ? 0 : count > 999 ? 999 : count);

        for (auto i = 0; i < static_cast<int>(rates.size()); ++i)
            rates[i] = measure_value(side, i);

        return rates;
    }

    auto add_link(avs2::node_ptr node) -> avs2::node_ptr
    {
        auto const link = avs2::property_node_create(nullptr, node, avs2::node_type_node, "tracker_link");

        if (link != nullptr)
            avs2::property_node_create(nullptr, link, avs2::node_type_attr, "ver@", META_PROJECT_VERSION);

        return link;
    }

    auto services_get_detour(void* ctx, avs2::node_ptr node) -> void*
    {
        try
        {
            // Read and hashed once; services.get is sent again after a network error.
            static auto const music_data = describe_music_data();

            if (!music_data.sha256.empty())
            {
                if (auto const link = add_link(node))
                {
                    avs2::property_node_create(nullptr, link, avs2::node_type_attr, "name@", music_data.name.c_str());
                    avs2::property_node_create(nullptr, link, avs2::node_type_attr, "size@", music_data.size.c_str());
                    avs2::property_node_create(nullptr, link, avs2::node_type_attr, "sha256@", music_data.sha256.c_str());
                    avs2::log::misc("tracker: added '{}' to services.get", music_data.name);
                }
            }
        }
        catch (...)
        {
        }

        return services_get_hook.call<void*>(ctx, node);
    }

    auto music_reg_detour(void* request, avs2::node_ptr node) -> bool
    {
        // The original builds the request first.
        auto const result = music_reg_hook.call<bool>(request, node);

        try
        {
            if (auto const link = add_link(node))
            {
                // Both lane sides: the tracker picks the player's side (or both for DP).
                auto lanes = std::array<std::uint8_t, 2 * lane_side_size> {};
                std::memcpy(lanes.data(), lane_counts, lane_side_size);
                std::memcpy(lanes.data() + lane_side_size, lane_counts + lane_side_stride, lane_side_size);

                avs2::property_node_create(nullptr, link, avs2::node_type_bin, "judge",
                    judge_counts, static_cast<std::uint32_t>(judge_counts_size));
                avs2::property_node_create(nullptr, link, avs2::node_type_bin, "lane",
                    lanes.data(), static_cast<std::uint32_t>(lanes.size()));

                if (auto const side = player_side(node); side >= 0 && get_timeline != nullptr)
                {
                    auto const rates = measure_rates(side);

                    if (!rates.empty())
                        avs2::property_node_create(nullptr, link, avs2::node_type_bin, "measure",
                            rates.data(), static_cast<std::uint32_t>(rates.size() * sizeof(float)));
                }
            }
        }
        catch (...)
        {
        }

        return result;
    }

    auto setup_services_get_hook(std::span<std::uint8_t> bm2dx) -> void
    {
        music_data_path = memory::find<const char*>(bm2dx,
            memory::to_pattern("/data/info/?/music_????.bin", '?'));

        // services.get request builder in avs2-ea3 2.17.4 (as upstream omnifix finds it).
        auto const ea3 = modules::find("avs2-ea3.dll");
        auto const target = memory::find(ea3.region(),
            "41 57 41 56 41 55 41 54 56 57 55 53 48 83 EC ? 48 89 D3");

        services_get_hook = safetyhook::create_inline(target, services_get_detour);

        if (!services_get_hook)
            throw error { "could not hook services.get" };
    }

    auto setup_music_reg_hook(std::span<std::uint8_t> bm2dx) -> void
    {
        // Judge_Apply: CALL DeadState_CountJudge ; CALL <getter> ; IMUL rdx,r13,11 ;
        // ... ; INC [rax + rdx*4 + 0x94]. The getter is LEA RAX,[g_DeadState] ; RET.
        auto const getter = memory::follow(memory::find(bm2dx,
            "E8 ? ? ? ? [E8] ? ? ? ? ? 6B ? 0B ? ? ? ? ? ? FF 84 ? 94 00 00 00"));

        if (!memory::find({ getter, getter + 8 }, "48 8D 05 ? ? ? ? C3", true))
            throw error { "unexpected DeadState getter at {:#x}", std::bit_cast<std::uintptr_t>(getter) };

        judge_counts = memory::follow(getter) + 0x94;

        // StageMain_AddPlayerNoteStats calls Score_GetLaneKindCount, which reads
        // LEA RCX,[g_ScoreBlock + 0x30] ; MOV EAX,[RCX + RAX*4] ; RET.
        auto const lane_fn = memory::follow(memory::find(bm2dx,
            "45 33 C0 8B CE 44 8B FE 41 8D 50 07 [E8] ? ? ? ?"));

        lane_counts = memory::follow(memory::find({ lane_fn, lane_fn + 0x40 },
            "[48] 8D 0D ? ? ? ? 8B 04 81 C3"));

        // StageResultDrawGraph::CopyGaugeHistory: CALL GetPtrTimeline ; MOV ecx,ebp ;
        // MOV edx,[rax+0x64] ; LEA eax,[rdx+1] ; MOV [r14],eax ; CALL CalcLastMeasureDjLevel ;
        // ... then the loop's CALL CalcMeasureValue(side, measure) ; MOVSS [rdi],xmm0.
        // Optional: without them music.reg just carries no <measure>.
        if (auto const site = memory::find(bm2dx,
                "[E8] ? ? ? ? 8B CD 8B 50 64 8D 42 01 41 89 06 E8 ? ? ? ? 41 8B 0E 83 F8 08", true))
        {
            if (auto const call = memory::find({ site, site + 0x60 }, "8B D3 8B CD [E8] ? ? ? ? F3 0F 11 07", true))
            {
                get_timeline = reinterpret_cast<timeline_fn>(memory::follow(site));
                measure_level = reinterpret_cast<level_fn>(memory::follow(site + 0x10));
                measure_value = reinterpret_cast<measure_fn>(memory::follow(call));
            }
        }

        if (get_timeline == nullptr)
            avs2::log::warning("tracker: score rate per measure not found, music.reg goes without it");

        // Xrpc_MusicReg_Serialize, found as upstream omnifix does: an instruction
        // pair inside it, then back to the start of the function.
        auto target = memory::find(bm2dx, "48 83 C0 ? 48 C7 44 24 28 00 03 00 00 48 89 44");
        target = memory::rfind({ bm2dx.data(), target }, "CC [48]");

        music_reg_hook = safetyhook::create_inline(target, music_reg_detour);

        if (!music_reg_hook)
            throw error { "could not hook music.reg" };
    }

    auto init() -> void
    {
        avs2::log::info("tracker_link v{} loaded", META_PROJECT_VERSION);

        auto const list = modules::list();
        auto const bm2dx = std::ranges::find_if(list, [] (auto&& entry)
            { return modules::has_export(entry.base, "dll_entry_main"); });

        if (bm2dx == list.end())
            throw std::runtime_error { "game library not found" };

        auto const region = bm2dx->region();
        reserve_hook_memory(region);

        try
        {
            setup_services_get_hook(region);
        }
        catch (const std::exception& e)
        {
            avs2::log::warning("tracker: music data report disabled: {}", e.what());
        }

        try
        {
            setup_music_reg_hook(region);
        }
        catch (const std::exception& e)
        {
            avs2::log::warning("tracker: judgment report disabled: {}", e.what());
        }
    }
}

auto DllMain(HINSTANCE module, DWORD reason, LPVOID) -> BOOL
{
    if (reason != DLL_PROCESS_ATTACH)
        return TRUE;

    DisableThreadLibraryCalls(module);

    try
    {
        init();
    }
    catch (const std::exception& e)
    {
        avs2::log::warning("tracker: not started: {}", e.what());
    }

    return TRUE;
}
