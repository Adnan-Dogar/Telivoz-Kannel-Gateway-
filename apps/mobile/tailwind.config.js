/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./src/**/*.{ts,tsx}"],
  presets: [require("nativewind/preset")],
  theme: {
    extend: {
      colors: {
        brand: { DEFAULT: "#4F46E5", soft: "#EEF2FF", dark: "#312E81" },
      },
    },
  },
  plugins: [],
};
