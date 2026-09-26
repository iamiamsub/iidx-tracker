// tracker_link.dll - gives an iidx-tracker in front of the server what the
// game's own traffic does not carry, by adding it to that traffic (the way
// upstream omnifix reports its hashes):
//
//   pc.get        <tracker_link ver@ name@ size@ sha256@ dxtra@/>
//                 the music data file the game loaded, so the tracker files the
//                 cabinet's plays under the right song list (a music ID can
//                 name different songs in different databases, and it tells
//                 omnimix from the regular game), and whether 2dxtra is loaded
//   music.reg     <tracker_link ver@><judge/><lane/><measure/></tracker_link>
//
// And one request of its own, through the game's ea3 (so still no network code here): on the music
// select it asks the tracker every second whether a song was picked on the tracker's page
// (tracker.poll, to the service iidx_tracker), and reserves that song the way the subscreen does.
//                 the play's judgments by timing (FAST/SLOW) and by key, and its
//                 score rate per measure, which the game keeps to itself
//   pc.save       <tracker_link ver@><custom_play .../>...</tracker_link>
//                 the credit's plays on charts 2dxtra swapped in (Kiraku, Kichiku,
//                 All-Scratch), whose music.reg 2dxtra holds back (see below)
//
// Only with a tracker in front of the server: the tracker adds a service named
// "iidx_tracker" to its answer to services.get, and the first time anything
// would be sent, ea3's service list is looked up for it. Without it nothing is
// added to any request until the game ends. The tracker takes <tracker_link>
// out before passing a request on. There is no network code here - nothing but
// the game's own requests carries the data.
// Load it with "-k tracker_link.dll"; it works with or without altfix.
// Part of IIDX Tracker; the game internals it reads are described in the
// comments below (addresses are for bm2dx 2026081900).

#include <array>
#include <atomic>
#include <cstdlib>
#include <algorithm>
#include <ctime>
#include <mutex>
#include <format>
#include <string>
#include <utility>
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
    // omnimix, so it is read when the first pc.get goes out, long after init.
    auto music_data_path = static_cast<const char*>(nullptr);

    // Whether a tracker is in front of the server; decided once, on first use.
    enum class tracker_state { unknown, present, absent };
    auto tracker = tracker_state::unknown;
    auto tracker_mutex = std::mutex {};

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

    auto pc_get_hook = SafetyHookInline {};
    auto music_reg_hook = SafetyHookInline {};

    // ---- 2dxtra charts ------------------------------------------------------
    //
    // 2dxtra (another hook DLL) can load charts it generated in place of the
    // game's (Kiraku, Kichiku, All-Scratch). For such a play it skips the call in
    // MusicReg_Dispatch that queues music.reg, so neither the server nor the
    // tracker hears of it. Here the play is collected where the dispatch adds it
    // to the credit's history, with 2dxtra telling which chart it was (its C API),
    // and the player's next pc.save (card out) carries it to the tracker.

    using kind_count_fn = int (*)(int side, int kind);   // Score_GetKindCount: 0 PG .. 4 POOR
    using miss_count_fn = int (*)(int side);             // Result_GetMissCount (-1: ended early or assisted)
    using play_state_fn = std::uint8_t* (*)();           // GetPtrPlayState
    using player_graph_fn = std::uint8_t* (*)(std::uint8_t* state, int side); // PlayState_GetPlayerGraph

    auto kind_count = static_cast<kind_count_fn>(nullptr);
    auto miss_count = static_cast<miss_count_fn>(nullptr);
    auto play_state = static_cast<play_state_fn>(nullptr);
    auto player_graph = static_cast<player_graph_fn>(nullptr);

    // g_PlayerBlock, one per side; the music_play_log block the dispatch fills
    // for music.reg lies inside it (iidx_id at +0, the rest as music.reg sends it).
    auto player_block = static_cast<std::uint8_t*>(nullptr);
    auto play_log_offset = std::int32_t {};
    auto constexpr player_block_stride = 0x3b103a0;
    auto constexpr play_log_size = 0x658;

    auto history_hook = SafetyHookInline {};
    auto pc_save_hook = SafetyHookInline {};

    struct custom_play
    {
        std::string iidx_id;
        std::vector<std::pair<std::string, std::string>> attrs;
        std::vector<std::pair<std::string, std::vector<std::uint8_t>>> bins;
    };

    // Collected on the game thread, sent from the network thread.
    auto pending_mutex = std::mutex {};
    auto pending = std::vector<custom_play> {};
    auto constexpr pending_max = 100;

    struct dxtra_api
    {
        bool (*is_custom_chart)(std::uint8_t player);
        const char* (*last_chart_id)(std::uint8_t player);
        std::uint32_t (*last_note_count)(std::uint8_t player);
    };

    /** 2dxtra's C API (api.h there), or nullptr while 2dxtra is not loaded. */
    auto dxtra() -> const dxtra_api*
    {
        static auto api = dxtra_api {};

        if (api.is_custom_chart == nullptr)
        {
            auto const module = GetModuleHandleA("2dxtra.dll");

            if (module == nullptr)
                return nullptr;

            api.last_chart_id = reinterpret_cast<decltype(api.last_chart_id)>(GetProcAddress(module, "Get_Last_Chart_ID"));
            api.last_note_count = reinterpret_cast<decltype(api.last_note_count)>(GetProcAddress(module, "Get_Last_Note_Count"));

            if (api.last_chart_id != nullptr && api.last_note_count != nullptr)
                api.is_custom_chart = reinterpret_cast<decltype(api.is_custom_chart)>(GetProcAddress(module, "Is_Custom_Chart"));
        }

        return api.is_custom_chart != nullptr ? &api : nullptr;
    }

    /**
     * avs2-ea3's lookup of a service by name in the list services.get answered (the one xrpc uses to
     * find where a module's requests go): without url it only says whether the service is listed.
     * Not exported - found by pattern (avs2-ea3 2.17, IIDX 32 / 33). Ordinal 29, taken for it at
     * first, looks up keepalive targets instead.
     */
    using service_listed_fn = bool (*)(const char* name, void* url);
    auto service_listed = static_cast<service_listed_fn>(nullptr);

    /**
     * Whether a tracker answered services.get: it lists a service "iidx_tracker". Asked the first
     * time anything would be sent (a login comes long after services.get); the answer holds until
     * the game ends.
     */
    auto tracker_present() -> bool
    {
        auto lock = std::scoped_lock { tracker_mutex };

        if (tracker == tracker_state::unknown)
        {
            tracker = service_listed != nullptr && service_listed("iidx_tracker", nullptr)
                ? tracker_state::present : tracker_state::absent;

            if (tracker == tracker_state::present)
                avs2::log::info("tracker: the tracker is in front of the server, reporting to it");
            else
                avs2::log::info("tracker: no tracker in front of the server, nothing will be added");
        }

        return tracker == tracker_state::present;
    }

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

    /** The lane table of both lane sides: the tracker picks the player's side (or both for DP). */
    auto both_lane_sides() -> std::array<std::uint8_t, 2 * lane_side_size>
    {
        auto lanes = std::array<std::uint8_t, 2 * lane_side_size> {};
        std::memcpy(lanes.data(), lane_counts, lane_side_size);
        std::memcpy(lanes.data() + lane_side_size, lane_counts + lane_side_stride, lane_side_size);
        return lanes;
    }

    auto add_link(avs2::node_ptr node) -> avs2::node_ptr
    {
        auto const link = avs2::property_node_create(nullptr, node, avs2::node_type_node, "tracker_link");

        if (link != nullptr)
            avs2::property_node_create(nullptr, link, avs2::node_type_attr, "ver@", META_PROJECT_VERSION);

        return link;
    }

    auto pc_get_detour(void* request, avs2::node_ptr node) -> std::uint64_t
    {
        auto const result = pc_get_hook.call<std::uint64_t>(request, node);

        try
        {
            // Every login carries it: the tracker keeps it for the cabinet until its next boot.
            if (tracker_present())
            {
                // Read and hashed once, at the first login.
                static auto const music_data = describe_music_data();

                if (auto const link = add_link(node))
                {
                    if (!music_data.sha256.empty())
                    {
                        avs2::property_node_create(nullptr, link, avs2::node_type_attr, "name@", music_data.name.c_str());
                        avs2::property_node_create(nullptr, link, avs2::node_type_attr, "size@", music_data.size.c_str());
                        avs2::property_node_create(nullptr, link, avs2::node_type_attr, "sha256@", music_data.sha256.c_str());
                    }

                    avs2::property_node_create(nullptr, link, avs2::node_type_attr, "dxtra@",
                        GetModuleHandleA("2dxtra.dll") != nullptr ? "1" : "0");
                    avs2::log::misc("tracker: added '{}' to pc.get", music_data.name);
                }
            }
        }
        catch (...)
        {
        }

        return result;
    }

    auto music_reg_detour(void* request, avs2::node_ptr node) -> bool
    {
        // The original builds the request first.
        auto const result = music_reg_hook.call<bool>(request, node);

        try
        {
            if (!tracker_present())
                return result;

            if (auto const link = add_link(node))
            {
                auto lanes = both_lane_sides();

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

    /** Collects a play on a 2dxtra chart; the arguments are PlayerData_AppendMusicHistory's. */
    auto collect_custom_play(int side, int music, int style, int chart, int ex, int lamp, int dj) -> void
    {
        auto const api = dxtra();

        if (api == nullptr || (side != 0 && side != 1) || !api->is_custom_chart(static_cast<std::uint8_t>(side)))
            return;

        auto const log = player_block + side * player_block_stride + play_log_offset;
        auto const iidx_id = std::string { reinterpret_cast<const char*>(log), strnlen(reinterpret_cast<const char*>(log), 16) };

        // A play without a card is not the tracker's business (and pc.save never comes).
        if (iidx_id.empty())
            return;

        auto play = custom_play { iidx_id };

        auto const attr = [&] (const char* name, const auto& value)
            { play.attrs.emplace_back(name, std::format("{}", value)); };

        auto const bin = [&] (const char* name, const std::uint8_t* data, std::size_t size)
            { play.bins.emplace_back(name, std::vector<std::uint8_t>(data, data + size)); };

        auto const chart_id = api->last_chart_id(static_cast<std::uint8_t>(side));

        attr("chart_id@", chart_id != nullptr ? chart_id : "");
        attr("notes@", api->last_note_count(static_cast<std::uint8_t>(side)));
        attr("side@", side);
        attr("mid@", music);
        attr("clid@", chart);
        attr("style@", style);
        attr("ex@", ex);
        attr("lamp@", lamp);
        attr("dj@", dj);
        attr("miss@", miss_count(side));
        attr("kinds@", std::format("{} {} {} {} {}", kind_count(side, 0), kind_count(side, 1),
            kind_count(side, 2), kind_count(side, 3), kind_count(side, 4)));

        // g_DeadState +0xec / +0xf4 / +0xfc: combo breaks, FAST, SLOW per lane side (DP: both).
        auto const dead = judge_counts - 0x94;
        auto const per_side = [&] (int offset)
        {
            auto const at = [&] (int lane_side) { return *reinterpret_cast<const std::int32_t*>(dead + offset + lane_side * 4); };
            return style == 1 ? at(0) + at(1) : at(side);
        };

        attr("combo@", per_side(0xec));
        attr("fast@", per_side(0xf4));
        attr("slow@", per_side(0xfc));

        // The score graph: EX per 1/64 of the notes (music.reg <ghost>) and the gauge (<ghost_gauge>).
        auto const graph = player_graph(play_state(), side);
        bin("log", log, play_log_size);
        bin("ghost", graph + 0x2a, 64);
        bin("gauge", graph + 0x6a, 768);

        auto const lanes = both_lane_sides();
        bin("judge", judge_counts, judge_counts_size);
        bin("lane", lanes.data(), lanes.size());

        if (get_timeline != nullptr)
        {
            if (auto const rates = measure_rates(side); !rates.empty())
                bin("measure", reinterpret_cast<const std::uint8_t*>(rates.data()), rates.size() * sizeof(float));
        }

        auto lock = std::scoped_lock { pending_mutex };

        if (pending.size() < pending_max)
            pending.push_back(std::move(play));
    }

    auto history_detour(int side, int music, int style, int chart, int ex, int lamp, int dj, char no_save) -> std::uint64_t
    {
        try
        {
            if (tracker_present())
                collect_custom_play(side, music, style, chart, ex, lamp, dj);
        }
        catch (...)
        {
        }

        return history_hook.call<std::uint64_t>(side, music, style, chart, ex, lamp, dj, no_save);
    }

    auto pc_save_detour(void* request, avs2::node_ptr node) -> std::uint64_t
    {
        auto const result = pc_save_hook.call<std::uint64_t>(request, node);

        try
        {
            if (!tracker_present())
                return result;

            // Only this player's plays: with two players each has a pc.save of its own.
            char iidx_id[32] {};

            if (avs2::property_node_refer(nullptr, node, "iidxid@", avs2::node_type_attr, iidx_id, sizeof(iidx_id)) < 0)
                return result;

            auto plays = std::vector<custom_play> {};
            {
                auto lock = std::scoped_lock { pending_mutex };
                auto const theirs = std::ranges::stable_partition(pending,
                    [&] (const custom_play& p) { return p.iidx_id != iidx_id; }).begin();

                plays.assign(std::make_move_iterator(theirs), std::make_move_iterator(pending.end()));
                pending.erase(theirs, pending.end());
            }

            if (plays.empty())
                return result;

            if (auto const link = add_link(node))
            {
                for (auto const& play: plays)
                {
                    auto const n = avs2::property_node_create(nullptr, link, avs2::node_type_node, "custom_play");

                    if (n == nullptr)
                        continue;

                    for (auto const& [name, value]: play.attrs)
                        avs2::property_node_create(nullptr, n, avs2::node_type_attr, name.c_str(), value.c_str());

                    for (auto const& [name, data]: play.bins)
                        avs2::property_node_create(nullptr, n, avs2::node_type_bin, name.c_str(),
                            data.data(), static_cast<std::uint32_t>(data.size()));
                }

                avs2::log::misc("tracker: added {} 2dxtra play(s) to pc.save", plays.size());
            }
        }
        catch (...)
        {
        }

        return result;
    }

    // ---- a song picked on the tracker's page ----
    //
    // On the music select tracker_link.dll asks the tracker about once a second, with a request of its
    // own (module "tracker", method "poll") that the game's ea3 sends to the service iidx_tracker - the
    // tracker answers it and relays nothing. A picked song is reserved the way the subscreen's search and
    // news reserve one (MusicReserveImpl::SetPrimary), and the music select jumps to it on its next frame
    // (MusicSelectWheel_JumpToReservedSong: opens the song's version folder, puts the cursor on the chart).

    /** An entry of an ea3 xrpc method table (as the game's, e.g. g_XrpcMethods_IIDX33lobby); a zeroed one ends it. */
    struct xrpc_method
    {
        const char* name;
        void* prep;
        void* serialize;
        void* parse;
        std::uint8_t flags[4];
        std::uint8_t pad[12];
    };
    static_assert(sizeof(xrpc_method) == 0x30);

    using module_add_fn = int (*)(const char* module, const char* service, int, const xrpc_method* methods);
    using xrpc_done_fn = void (*)(void* data, std::uint64_t status, void* arg);
    using xrpc_apply_fn = int (*)(void* xrpc, const char* method, int, void* data, xrpc_done_fn done, void* arg, void* args);
    using reserve_fn = void (*)(void* reserve, int music, int style, int difficulty);

    auto module_add = static_cast<module_add_fn>(nullptr); // avs2-ea3 ordinal 55, as Jnet2_RegisterXrpcModules uses it
    auto xrpc_apply = static_cast<xrpc_apply_fn>(nullptr); // avs2-ea3 ordinal 59: < 0 = not sent
    auto jnet2 = static_cast<void**>(nullptr); // g_Jnet2Ctx; +0 is the game's xrpc handle
    auto reserve_instance = static_cast<void* (*)()>(nullptr); // MusicReserveImpl::GetInstance
    auto is_double_play = static_cast<int (*)()>(nullptr); // IsDoublePlay: the style being played
    auto music_select_hook = SafetyHookInline {};

    // the picked song, from the poll's answer (an ea3 thread) to the music select (the game's thread)
    auto picked_music = std::atomic<int> { -1 };
    auto picked_chart = std::atomic<int> { -1 };
    auto polling = std::atomic<bool> { false };
    auto next_poll = std::uint64_t { 0 };
    enum class poll_state { unregistered, ready, failed };
    auto poll_module = poll_state::unregistered;

    auto poll_prep(void*, void*) -> int { return 1; }

    auto poll_serialize(void*, avs2::node_ptr) -> int { return 1; } // nothing to send: the call's srcid names the cabinet

    /** The answer: <tracker music_id@ chart@> (the game's chart index, SPB..SPL, DPB..DPL) when a song was picked. */
    auto poll_parse(void*, avs2::node_ptr node) -> int
    {
        auto const read = [node] (const char* name)
        {
            char value[16] {};
            return avs2::property_node_refer(nullptr, node, name, avs2::node_type_attr, value, sizeof(value)) < 0
                ? -1 : std::atoi(value);
        };

        if (auto const music = read("music_id@"); music > 0)
        {
            picked_chart = read("chart@");
            picked_music = music;
        }

        return 1;
    }

    auto poll_done(void*, std::uint64_t, void*) -> void { polling = false; }

    const xrpc_method poll_methods[] {
        { "poll", reinterpret_cast<void*>(poll_prep), reinterpret_cast<void*>(poll_serialize),
          reinterpret_cast<void*>(poll_parse), { 1, 1, 1, 1 }, {} },
        {},
    };

    /** MusicSelectWheel_JumpToReservedSong: every frame the music select takes input. */
    auto music_select_detour(void* scene, void* input) -> void
    {
        try
        {
            if (tracker_present())
            {
                if (auto const music = picked_music.exchange(-1); music > 0)
                {
                    auto const chart = picked_chart.load();
                    auto const reserve = reserve_instance();

                    // style and difficulty as the subscreen passes them (Chart_StyleFromIndex /
                    // Chart_DifficultyFromIndex), but in the style being played: the music select does not
                    // jump to a chart of the other one, so an SPA picked while playing DP goes to the DPA
                    (*reinterpret_cast<reserve_fn**>(reserve))[0](reserve, music, is_double_play() ? 1 : 0, chart >= 0 ? chart % 5 : -1);
                    avs2::log::info("tracker: jumping to {} (chart {}) picked on the tracker's page", music, chart);
                }

                if (poll_module == poll_state::unregistered)
                {
                    poll_module = module_add("tracker", "iidx_tracker", 0, poll_methods) == 0 ? poll_state::ready : poll_state::failed;

                    if (poll_module == poll_state::failed)
                        avs2::log::warning("tracker: cannot register the poll, songs cannot be picked from the tracker");
                }

                auto const now = GetTickCount64();

                if (poll_module == poll_state::ready && !polling && now >= next_poll && *jnet2 != nullptr)
                {
                    next_poll = now + 1000;
                    polling = true;

                    if (xrpc_apply(*jnet2, "tracker.poll", 0, nullptr, poll_done, nullptr, nullptr) < 0)
                        polling = false;
                }
            }
        }
        catch (...)
        {
        }

        music_select_hook.call<void>(scene, input);
    }

    auto setup_pick_hook(std::span<std::uint8_t> bm2dx) -> void
    {
        auto const ea3 = GetModuleHandleA("avs2-ea3.dll");

        module_add = reinterpret_cast<module_add_fn>(GetProcAddress(ea3, MAKEINTRESOURCEA(55)));
        xrpc_apply = reinterpret_cast<xrpc_apply_fn>(GetProcAddress(ea3, MAKEINTRESOURCEA(59)));

        if (module_add == nullptr || xrpc_apply == nullptr)
            throw error { "avs2-ea3 has no xrpc_apply" };

        // Jnet_Init passes g_Jnet2Ctx on
        jnet2 = reinterpret_cast<void**>(memory::follow(memory::find(bm2dx, "[48] 8D 0D ? ? ? ? 49 89 73 D0 33")));

        auto const target = memory::find(bm2dx, "48 89 54 24 10 48 89 4C 24 08 55 53 56 57 41 54 41 55 41 56 41 57 "
            "48 8D 6C 24 E1 48 81 EC C8 00 00 00 48 C7 45 EF FE FF FF FF 48 8B D9 E8 ? ? ? ? 85 C0 0F 85 ? ? ? ? "
            "E8 ? ? ? ? 80 78 09 00");
        reserve_instance = reinterpret_cast<void* (*)()>(memory::follow(target + 0x3a)); // right before it checks [+9]
        // MusicSelect_ApplyJumpMusic asks it before looking up a folder
        is_double_play = reinterpret_cast<int (*)()>(memory::follow(memory::find(bm2dx,
            "48 8B 5C 24 48 [E8] ? ? ? ? 44 8B C5 8B D0 48 8B CB E8")));

        music_select_hook = safetyhook::create_inline(target, music_select_detour);

        if (!music_select_hook)
            throw error { "could not hook the music select" };
    }

    auto setup_pc_get_hook(std::span<std::uint8_t> bm2dx) -> void
    {
        music_data_path = memory::find<const char*>(bm2dx,
            memory::to_pattern("/data/info/?/music_????.bin", '?'));

        // Xrpc_PcGet_Serialize: the refid, dataid and cardid it formats in a row, then back to
        // its start (no padding before it to search for).
        auto const body = memory::find(bm2dx, "48 83 C0 08 48 8D 54 24 ? 48 8B C8 E8 ? ? ? ? 48 8B 44 24 ? "
            "48 8D 54 24 ? 48 8B C8 E8 ? ? ? ? 48 8B 44 24 ? 48 83 C0 20 48 8D 54 24 ? 48 8B C8 E8");
        auto const target = memory::rfind({ body - 0x80, body }, "48 89 54 24 10 48 89 4C 24 08 48 81 EC");

        pc_get_hook = safetyhook::create_inline(target, pc_get_detour);

        if (!pc_get_hook)
            throw error { "could not hook pc.get" };
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

    auto setup_custom_play_hooks(std::span<std::uint8_t> bm2dx) -> void
    {
        if (judge_counts == nullptr || lane_counts == nullptr)
            throw error { "the judgment tables were not found" };

        // MusicReg_Dispatch counts the judgments: ... MOV EDX,2 ; MOV [RBP+x],EAX ; MOV ECX,R12D ;
        // CALL Score_GetKindCount ; MOV EDX,3 ...
        kind_count = reinterpret_cast<kind_count_fn>(memory::follow(memory::find(bm2dx,
            "BA 02 00 00 00 89 45 ? 41 8B CC [E8] ? ? ? ? BA 03 00 00 00")));

        // Result_GetMissCount: PUSH RDI ; SUB RSP,20h ; MOV EDI,ECX ; CALL GetPtrDeadState ; ...
        miss_count = reinterpret_cast<miss_count_fn>(memory::find(bm2dx,
            "40 57 48 83 EC 20 8B F9 E8 ? ? ? ? 8B D7 48 8B C8 E8 ? ? ? ? 84 C0 75"));

        // MusicReg_Dispatch's <ghost>: CALL GetPtrPlayState ; MOV EDX,side ; MOV RCX,RAX ;
        // CALL PlayState_GetPlayerGraph ; ADD RAX,2Ah
        auto const graph = memory::find(bm2dx, "[E8] ? ? ? ? ? ? ? 48 8B C8 E8 ? ? ? ? 48 83 C0 2A 48 89");
        play_state = reinterpret_cast<play_state_fn>(memory::follow(graph));
        player_graph = reinterpret_cast<player_graph_fn>(memory::follow(graph + 11));

        // MusicPlayLog_Fill (the first of two alike functions): IMUL RBX,RAX,3B103A0h ;
        // LEA RAX,[g_PlayerBlock] ; ADD RBX,RAX ; MOV EAX,[RBX+3B10398h] ; TEST EAX,EAX ; JNE ;
        // LEA RCX,[RBX + music_play_log] - the block the iidx_id is copied into.
        auto const block = memory::find(bm2dx,
            "48 69 D8 A0 03 B1 03 [48] 8D 05 ? ? ? ? 48 03 D8 8B 83 98 03 B1 03 85 C0 75 ? 48 8D 8B ? ? ? ?");
        player_block = memory::follow(block);
        play_log_offset = *reinterpret_cast<const std::int32_t*>(block + 23);

        // PlayerData_AppendMusicHistory(side, music, style, chart, ex, lamp, dj level, no save),
        // called for every side at the end of MusicReg_Dispatch - also when 2dxtra skipped music.reg.
        auto const history = memory::find(bm2dx,
            "48 83 EC 28 45 8B D0 44 8B DA E8 ? ? ? ? 85 C0 0F 85 ? ? ? ? 48 63 C1 48 69 C8 A0 03 B1 03 "
            "48 8D 05 ? ? ? ? 83 BC 01 ? ? ? ? 01 0F 84 ? ? ? ? 80 BC 01 ? ? ? ? 00 0F 84 ? ? ? ? "
            "4C 8D 80 ? ? ? ? 4C 03 C1 49 63 80 ? ? ? ? 83 F8 09");

        // Xrpc_PcSave_Serialize(request, node)
        auto const pc_save = memory::find(bm2dx, "48 89 54 24 10 48 89 4C 24 08 48 81 EC 28 04 00 00");

        history_hook = safetyhook::create_inline(history, history_detour);
        pc_save_hook = safetyhook::create_inline(pc_save, pc_save_detour);

        if (!history_hook || !pc_save_hook)
        {
            history_hook = {};
            pc_save_hook = {};
            throw error { "could not hook the play history or pc.save" };
        }
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
            service_listed = memory::find<service_listed_fn>(modules::find("avs2-ea3.dll").region(),
                "41 57 41 56 41 54 56 57 55 53 48 83 EC 50 48 89 CB 8B 0D ? ? ? ? 85 C9 7E ? 49 89 D4 E8");
        }
        catch (const std::exception& e)
        {
            avs2::log::warning("tracker: cannot look for the tracker, nothing will be added: {}", e.what());
        }

        try
        {
            setup_pc_get_hook(region);
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

        try
        {
            setup_custom_play_hooks(region);
        }
        catch (const std::exception& e)
        {
            avs2::log::warning("tracker: 2dxtra chart report disabled: {}", e.what());
        }

        try
        {
            setup_pick_hook(region);
        }
        catch (const std::exception& e)
        {
            avs2::log::warning("tracker: picking songs from the tracker disabled: {}", e.what());
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
