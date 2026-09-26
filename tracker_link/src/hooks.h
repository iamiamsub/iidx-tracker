#pragma once

// Hook helpers shared by altfix and tracker_link: the near-memory reservation
// every game hook needs, and a registry of installed hooks so a feature that
// fails partway can take its hooks back out (see try_feature).

#include <bit>
#include <deque>
#include <span>
#include <format>
#include <cstdint>
#include <stdexcept>
#include <windows.h>
#include <safetyhook.hpp>

#include "avs2.h"
#include "exceptions.h"

namespace omnifix
{
    // Installed hooks. Destroying one removes it, so erasing back to an earlier
    // size undoes everything installed since (a deque never moves its elements).
    inline auto mid_hooks = std::deque<safetyhook::MidHook> {};
    inline auto inline_hooks = std::deque<safetyhook::InlineHook> {};

    /** Installs a mid hook, or throws when safetyhook could not place it. */
    inline auto add_mid_hook(void* target, safetyhook::MidHookFn fn) -> void
    {
        auto hook = safetyhook::create_mid(target, fn);

        if (!hook)
            throw error { "could not hook {:#x}", std::bit_cast<std::uintptr_t>(target) };

        mid_hooks.push_back(std::move(hook));
    }

    /** Installs an inline hook whose detour never calls through the hook object itself. */
    inline auto add_inline_hook(void* target, auto detour) -> void
    {
        auto hook = safetyhook::create_inline(target, detour);

        if (!hook)
            throw error { "could not hook {:#x}", std::bit_cast<std::uintptr_t>(target) };

        inline_hooks.push_back(std::move(hook));
    }

    /**
     * Hands safetyhook a block of memory right after the game image before any
     * game hook is created.
     *
     * safetyhook 0.6.9 finds memory near a hook target by walking down from it,
     * then by trying the start of each free region above it - and that start is
     * not 64K aligned right after the image, so the allocation fails there. spice
     * loads the game low (e.g. 0x10580000) with everything below already taken,
     * so both searches fail, every hook falls back to a 14-byte absolute jump, and
     * any hook whose stolen bytes hold a relative instruction (LEA [rip], CALL,
     * Jcc) is silently dropped - e.g. the ssm label hook, which left INFINITAS
     * songs showing the previous song's version logo. Later near allocations are
     * served from this block's free list.
     *
     * @param image The game image.
     */
    inline auto reserve_hook_memory(std::span<std::uint8_t> image) -> void
    {
        // Keeps the block - and the global allocator, only weakly held - alive.
        static auto block = safetyhook::Allocation {};

        SYSTEM_INFO si {};
        GetSystemInfo(&si);

        auto const granularity = std::uintptr_t { si.dwAllocationGranularity };

        // Any block starting below this ends within rel32 reach of the whole image.
        auto const limit = std::bit_cast<std::uintptr_t>(image.data()) + 0x7ffe0000;

        MEMORY_BASIC_INFORMATION mbi {};

        for (auto p = std::bit_cast<std::uintptr_t>(image.data() + image.size());
             p < limit && VirtualQuery(std::bit_cast<void*>(p), &mbi, sizeof(mbi));
             p = std::bit_cast<std::uintptr_t>(mbi.BaseAddress) + mbi.RegionSize)
        {
            auto const slot = (p + granularity - 1) & ~(granularity - 1);
            auto const end = std::bit_cast<std::uintptr_t>(mbi.BaseAddress) + mbi.RegionSize;

            if (mbi.State != MEM_FREE || slot + granularity > end)
                continue;

            // A max distance of one granule keeps safetyhook from handing back
            // some unrelated block it already owns.
            if (auto a = safetyhook::Allocator::global()->allocate_near(
                    { std::bit_cast<std::uint8_t*>(slot) }, 1, granularity))
            {
                block = std::move(*a);
                avs2::log::info("hook memory reserved at {:#x}", slot);
                return;
            }
        }

        avs2::log::warning("no free memory near the game image, hooks on relative instructions may fail");
    }
}
