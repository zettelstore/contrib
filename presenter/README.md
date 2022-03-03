*Zettel Presenter* creates a slide show, handouts and more from zettel maintained by a [Zettelstore](https://zettelstore.de).

## Build instructions
Just enter `go build .` within the directory of this sub-project and you will find an executable called `presenter`.

## Run instructions
    # presenter -h
    Usage of presenter:
      -a    Zettelstore needs authentication
      -l string
            Listen address (default ":23120")
      [URL] URL of Zettelstore (default: "http://127.0.0.1:23123")

* `URL` denotes the base URL of the Zettelstore, where the slide zettel are stored.
* `-a` signals that the Zettelstore needs authentication. Username and password are requested at the command line where you started zettel presenter.
* `-l` specifies the listen address, to allow to connect to zettel presenter with your browser. If you use the default value, you must point your browser to <http://127.0.0.1:23120>.

## Configuration
Further configuration is stored in the metadata of a zettel with the special identifier [00009000001000](https://zettelstore.de/manual/h/00001006055000).
Currently, two keys are supported:

* `slideset-role` specifies the [zettel role](https://zettelstore.de/manual/h/00001006020100) a zettel must have to be recognized as a starting point of a slide set. The default value is "slideset".
* `author` specifies the default value for the author value of slide shows. Its default value is the empty string, which omits all author information.

## Slide set
A slide set is a zettel, which is marked with a zettel role of the value given by the configuration key `slideset-role`(default: slideset, see above).
Its main purpose is to list all zettel that should act a slides for a slide show.
In other word, its is basically a table of contents.

Internally, zettel presenter tries to find the first list of the slide set zettel, either an ordered or unordered list.
For all first-level list items, zettel presenter investigates the very first [link reference](https://zettelstore.de/manual/h/00001007040310).
If this reference point to a zettel, this zettel will be part of the slide set.

Of course, it is allowed to reference the same zettel more than one time, if you reference it in different first-level items of the slide set zettel.

The second purpose of the slide set zettel is to specify data needed for a slide show / handout.
This data is stored inside the metadata of the zettel:

* `slide-title` specifies the title of the presentation. Its default value is the value of the zettel title. This allows to specify a special value for the presentation.
* `sub-title` denotes a sub-title. If not given, no default value is produced for the presentation / handout. As with `slide-title`, you are allowed to make use of Zettelmarkup's [inline-structured elements](https://zettelstore.de/manual/h/00001007040000), i.e. text formatting.
* `author` names the author of the slide set, defaulting to the same value of the configuration zettel (see above).
* `copyright` produces a copyright statement. If not specified, Zettelstore itself will provide a [default value](https://zettelstore.de/manual/h/00001004020000#default-copyright).
* `license` allows to specify a license text. Similar to `copyright`, Zettelstore will provide a [default value](https://zettelstore.de/manual/h/00001004020000#default-license).

## Slide
A slide is just a zettel referenced by slide set zettel.
Zettel Presenter does not enforce a special zettel role.
However, it is good practice to use a special zettel role, i.e. "slide".
This makes it easier to find a specific slide by listing only zettel of the zettel role.

Similar to a slide set zettel, zettel presenter looks at the metadata of a slide zettel:

* `slide-title` allows to overwrite the title of the zettel for the purpose of creation a presentation.
* `slide-role` allows to mark a slide zettel to be included only for either a slide show (value must be "show") or a handout (value must be "handout"). If no value is given, the slide will included in all presentations. If another value is given, the slide will not be part of any presentation document.

## Slide roles
Currently, two slide roles are implemented: a slide show and a handout.

Presenting a slide show is the main use case of zettel presenter.
All relevant slides are collected, and a HTML-based slide show is produced.

The handout is another HTML document, that contains all relevant slides.
There are no slide show elements, all slides content is shown in a linear way.
Referenced zettel that are not part of the slide set, but have the [visibility](https://zettelstore.de/manual/h/00001010070200) "public", are added at the end of the slide set for further reference.
This document can be given to your audience, without risking to give away confidential material.

If you reference a zettel of the same slide set, an appropriate HTML link will be produced.
Since a zettel may occur more than once in a slide set, referenced zettel are first searched backwards.

If you reference a zettel outside your slide set, it will be linked in the slide show.
By following this link, the referenced zettel will shown, but not in the context of the slide show.
As written above, such a link will only be produced in the handout, if the zettel visibility is "public".
In this case, it is part of the slide set.

## Navigating
Zettel presenter operates in a simple way.
It used the same zettel identifier as Zettelstore uses.
If no zettel identifier is provided in the URL, zettel presenter shows the [home zettel](https://zettelstore.de/manual/h/00001004020000#home-zettel) of Zettelstore.

If the zettel is a slide set, all relevant zettel are collected to be used in a slide show / handout.
These zettel are presented in a numbered / ordered list.
If you follow the link of such a list item, you will be directed to the given slide in a slide show.

At the bottom of the presented slide set, there is a link to produce the handout.

If the zettel is not a slide set zettel, it is shown in a relative straight-forward way, very roughly similar to the view of a zettel within the Zettelstore web user interface.
This allows you to show additional content (if linked from a slide), or allows you to navigate to a slide set zettel to start a presentation.