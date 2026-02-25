# PKl Generator

The PKL Generator is an experimental approach to generate PKL schema from the stored formae resource. It uses quite a lot of tricks to generate some reliable PKL output that can be reused by formae. It's not fully robust to errors that are provided by the PKL schema. If there is an issue in the schema it might lead to failure in generation.

# Helper Tools

There are two scripts that are very helpful to the PKL Generator:

1. `split.py`: This script is used to split the input JSON files into smaller, more manageable pieces. This can be useful for large files that may cause issues during processing.

2. `run_generator.py`: This script is responsible for running the PKL Generator on the split JSON files and managing the overall generation process. It handles errors and logging, making it easier to track the progress of the generation.


The workflow should look like this:

(make sure that formae is built with these `make build build-debug build-pkl-local` and run `pkl project resolve` in the generator directory)

1. Extract the resources in JSON format `formae extract --output-schema json --query="managed:false" --output-consumer machine`
2. Use the `split.py` script to split the extracted JSON files into smaller pieces.
3. Run the `run_generator.py` script to generate the PKL schema from the split JSON files.

**Keep in mind that when running the PKL generator all of the files needs to be in the same directory or in a sub directory. PKL can't access files outside of these locations.**