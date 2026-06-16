<p align="center">
  <img src="media/readme/owpengram_splash.png" alt="OwpenGram" width="440">
</p>

# 🚀 OwpenGram Server

**Your own private messaging server — up and running in one command.**

OwpenGram is an open-source, Telegram-compatible messaging platform you fully
own. Run it on your laptop for a private network, or on a VPS to be reachable
anywhere in the world. Your data, your keys, your rules — no cloud, no lock-in,
no censorship.

> 🔗 Implements **MTProto API layer 222**.

<p align="center">
  <img src="media/readme/server_hero.png" alt="OwpenGram server: Docker stack and the one-command start script" width="900">
</p>

---

## ✨ Why OwpenGram?

- 🔒 **Private & self-hosted** — messages live on infrastructure you control.
- 🧩 **Telegram-compatible** — works with the familiar OwpenGram clients.
- 🌍 **Reachable anywhere** — host it globally, or keep it on your own network.
- 🛡️ **Censorship-resistant** — no central authority can shut you down.
- 🆓 **Free & open source** — Apache-2.0, audit and extend it freely.

## 🎯 What works today

- 💬 Private chats
- 👥 Groups & channels
- 📞 Voice & video calls (1-to-1, with a built-in TURN relay so they work globally)
- 🖼️ Media & files — photos, videos, documents
- 📇 Contacts sync
- 🌐 Android & Desktop clients

## ⚡ Quick Start

All you need is **[Docker](https://docs.docker.com/get-docker/)**. One script
sets up everything — database, cache, media storage, the server, and the calls
relay.

**1. Clone the repository**

```bash
git clone https://github.com/owpengram/owpengram-server.git
cd owpengram-server
```

**2. Run the start script**

- **Linux / macOS:** `./start-server.sh`
- **Windows:** `start-server.bat`

It asks for your **public address** — this one answer decides whether your
server is private or reachable worldwide:

- 🏠 **Local network:** enter your machine's local IP (e.g. `192.168.1.50`).
  Clients on the same network connect to it.
- 🌍 **Global (VPS):** enter your server's public IP or domain
  (e.g. `203.0.113.10` or `chat.example.com`).

The script then generates a TURN secret, bakes your address into the config,
builds and starts the whole stack (plus the calls relay), and opens the needed
Windows firewall ports automatically.

> **Default verification code:** `12345` — change it before any real use!

## 🔌 Ports to open for global access

When hosting on a VPS, open these in your provider's firewall (and the OS firewall):

- `10443` **TCP** — MTProto (login, chats, media)
- `3478` **UDP + TCP** — TURN/STUN (call setup)
- `49160–49200` **UDP** — TURN media relay (call audio/video)

For local-network use you don't need to open anything beyond your LAN.

## 📱 Connect a client

Install a client and add your server in it:

- **Host / IP:** the address you entered in the start script
- **Port:** `10443`
- **RSA key:** leave empty — the default build trusts the bundled OwpenGram key

Clients:

- 🤖 [Android client](https://github.com/owpengram/owpengram-android-client)
- 💻 [Desktop client](https://github.com/owpengram/owpengram-desktop-client)

## 💬 Community

- 📢 Channel: [@owpengram](https://t.me/owpengram)
- 💬 Chat: [Join the discussion](https://t.me/+sVB6Ymv70jEwNTAy)

OwpenGram builds on the excellent
[Teamgram Server](https://github.com/teamgram/teamgram-server) — see it for deep
architecture documentation.

## 📄 License

[Apache License 2.0](LICENSE)

---

⭐ If OwpenGram is useful to you, a star on GitHub helps the project grow.
