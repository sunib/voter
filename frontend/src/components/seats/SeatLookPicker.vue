<script setup lang="ts">
// A colour from the palette and an emoji from the list: the only two things a
// seat may change. Both lists are the page's offer; the CRD holds the emoji to
// exactly these and the colour to any #rrggbb.
import { SEAT_COLORS, SEAT_EMOJIS } from '../../api/seats'

const color = defineModel<string>('color', { required: true })
const emoji = defineModel<string>('emoji', { required: true })
</script>

<template>
  <fieldset class="seat-look">
    <legend class="seat-look__legend">Your colour</legend>
    <div class="seat-look__colors">
      <button
        v-for="option in SEAT_COLORS"
        :key="option.hex"
        type="button"
        class="seat-look__color"
        :class="{ 'seat-look__choice--on': color === option.hex }"
        :style="{ '--seat-color': option.hex }"
        :aria-label="option.name"
        :aria-pressed="color === option.hex"
        @click="color = option.hex"
      />
    </div>
  </fieldset>
  <fieldset class="seat-look">
    <legend class="seat-look__legend">Your emoji</legend>
    <div class="seat-look__emojis">
      <button
        v-for="option in SEAT_EMOJIS"
        :key="option"
        type="button"
        class="seat-look__emoji"
        :class="{ 'seat-look__choice--on': emoji === option }"
        :aria-pressed="emoji === option"
        @click="emoji = option"
      >
        {{ option }}
      </button>
    </div>
  </fieldset>
</template>
