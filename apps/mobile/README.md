# Telivoz mobile app

React Native app (Expo, TypeScript, NativeWind/Tailwind) for staff and clients of the Telivoz SMS gateway.

Screens:

- **Dashboard**: messages, delivery rate, revenue/margin (staff) or spend (clients), failures, with a trend chart for 24 h, 7 or 30 days.
- **Live**: messages per second in/out/DLR for the last 2 minutes, plus queue depth, vendor binds and bound clients.
- **Connections** (staff): vendor SMPP bind status, sent/error counters, and restart.
- **Messages**: search by number, sender, text or ID, with a status filter and per-message delivery details.
- **Account**: profile, 2FA status, light/dark mode, sign out.

It uses the same API as the web portal. Sign-in asks the server for a session token (`"token": true`), keeps it in the device keychain (Keychain/Keystore via `expo-secure-store`) and sends it as `Authorization: Bearer`. Accounts with two-factor sign-in are asked for their 6-digit code.

## Develop

```bash
npm ci
npm start            # scan the QR code with Expo Go, or press a / i for an emulator
npm run typecheck
```

On the sign-in screen, enter the portal address (for example `https://portal.example.com`). Use HTTPS in production.

## Build for the stores

Builds use EAS (no Xcode/Android Studio needed):

```bash
npx eas-cli@latest build --platform android   # or ios
```

`ios/` and `android/` are generated from `app.json`; do not edit them by hand.
