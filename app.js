let fs = require('fs');
let readLastLines = require('read-last-lines');
let app = require('express')();
let http = require('http').createServer(app);
let ioOpts = {
    'origins': "*:*", // cors
    'pingTimeout': 5000,
    'pingInterval': 5000,
};
let io = require('socket.io')(http, ioOpts);
let peerMap = {};

io.on('connection', (socket) => {
    let peerID = socket.conn.id
    let id = socket.conn.id + "@" + socket.conn.remoteAddress
    console.log(id, 'connected')
    socket.on('disconnecting', (reason) => {
        console.log(id, 'disconnecting', reason)
        Object.keys(socket.rooms).forEach(async (room) => {
            if (peerMap[room] !== undefined) {
                room = "_general"
            }
            logToFile(room, socket, 'on:disconnecting', {'reason': reason})
            socket.leave(room)
            let roomPeers = await allRoomPeers(room);
            let out = {
                'room': room,
                'peer': peerMap[peerID],
                'peers': roomPeers,
            }
            logToFile(room, socket, 'event:disconnect', out)
            io.to(room).emit('event:disconnect', out)
        });
    })
    socket.on('disconnect', () => {
        logToFile('_general', socket, 'on:disconnect', {})
        console.log(id, 'disconnect')
        delete peerMap[id];
    })
    socket.on('error', (error) => {
        console.log(id, 'socket.error')
        logToFile('_general', socket, 'on:error', error)
    })
    socket.on('join', (event) => {
        console.log(id, 'join', JSON.stringify(event))
        logToFile(event.room, socket, 'on:join', event)
        peerMap[peerID] = event.peer
        socket.join(event.room, async () => {
            let roomPeers = await allRoomPeers(event.room);
            let out = {
                'room': event.room,
                'peer': peerMap[peerID],
                'peers': roomPeers,
            }
            logToFile(event.room, socket, 'event:join', out)
            io.to(event.room).emit('event:join', out)
            let limit = Math.min(event.max_log_entries, 50);
            readLastLines.read('log-'+event.room+'.txt', limit)
                .then((lines) => {
                    lines.split(/\r?\n/).forEach((line) => {
                        if (line.length < 1) {
                            return
                        }
                        var evt = JSON.parse(line);
                        switch (evt.kind) {
                        case 'event:broadcast': break;
                        //case 'event:join': break;
                        default: return;
                        };
                        evt.data.is_live = false
                        socket.emit(evt.kind, evt.data)
                    });
                });
        })
    })
    socket.on('broadcast', (event) => {
        console.log(id, 'broadcast', JSON.stringify(event))
        logToFile(event.room, socket, 'on:broadcast', event)
        let out = {
            room: event.room,
            msg: event.msg,
            peer: peerMap[peerID],
            is_live: true,
        }
        logToFile(event.room, socket, 'event:broadcast', out)
        io.to(event.room).emit('event:broadcast', out)
    })
})

let logToFile = (room, socket, kind, data) => {
    let line = {
        'date': Date.now(),
        'room': room,
        'id': socket.conn.id,
        'peer': peerMap[socket.conn.id],
        'addr': socket.conn.remoteAddress,
        'kind': kind,
        'data': data,
    }
    fs.appendFile('log-'+room+'.txt', JSON.stringify(line) + "\n", (err) => {
        if (err) console.error('log', line)
    })
}

async function allRoomPeers(room) {
    let promise = new Promise((res, rej) => {
        io.in(room).clients((error, clients) => {
            if (error) throw error;
            res(clients)
        });
    });
    let clients = await promise;

    let peers = []
    clients.forEach(id => peers.push(peerMap[id]))

    return peers;
}

io.on('error', (error) => {
    console.log('io.error', error)
})

http.listen(3000, () => {
    console.log('listening on *:3000')
})
